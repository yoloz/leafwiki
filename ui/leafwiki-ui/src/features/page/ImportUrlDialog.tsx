import BaseDialog, { BaseDialogConfirmButton } from '@/components/BaseDialog'
import { FormInput } from '@/components/FormInput'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import i18next from '@/lib/i18n'
import {
  DIALOG_IMPORT_URL,
} from '@/lib/registries'
import { buildEditUrl } from '@/lib/routePath'
import {
  importPageFromURL,
  importPageFromURLCreate,
  suggestSlug,
  summarizeContent,
} from '@/lib/api/pages'
import { handleFieldErrors } from '@/lib/handleFieldErrors'
import { mapApiError } from '@/lib/api/errors'
import { useConfigStore } from '@/stores/config'
import { useDialogsStore } from '@/stores/dialogs'
import { useTreeStore } from '@/stores/tree'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'
import type { PageNode } from '@/lib/api/pages'

type ParentOption = { value: string; label: string }

function buildParentOptions(root: PageNode | null): ParentOption[] {
  const opts: ParentOption[] = [{ value: '', label: i18next.t('importDialog.root', { ns: 'page' }) }]
  if (!root) return opts
  const walk = (node: PageNode, depth: number) => {
    for (const ch of node.children || []) {
      const indent = '\u00A0\u00A0\u00A0\u00A0'.repeat(depth)
      opts.push({ value: ch.id, label: `${indent}${ch.title}` })
      walk(ch, depth + 1)
    }
  }
  walk(root, 0)
  return opts
}

export function ImportUrlDialog() {
  const { t } = useTranslation('page')
  const [url, setURL] = useState('')
  const [parentId, setParentId] = useState('')
  const [title, setTitle] = useState('')
  const [content, setContent] = useState('')
  const [fetching, setFetching] = useState(false)
  const [summarizing, setSummarizing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})

  const llmConfigured = useConfigStore((s) => s.llmConfigured)
  const tree = useTreeStore((s) => s.tree)
  const reloadTree = useTreeStore((s) => s.reloadTree)
  const getPathById = useTreeStore((s) => s.getPathById)
  const navigate = useNavigate()

  const parentOptions = useMemo(() => buildParentOptions(tree), [tree])

  const contentLoaded = content.trim().length > 0

  const resetForm = () => {
    setURL('')
    setParentId('')
    setTitle('')
    setContent('')
    setFetching(false)
    setSummarizing(false)
    setSaving(false)
    setFieldErrors({})
  }

  const handleFetch = async (): Promise<boolean> => {
    if (!url.trim()) {
      setFieldErrors((p) => ({ ...p, url: t('importDialog.urlRequired') }))
      return false
    }
    setFetching(true)
    setFieldErrors((p) => ({ ...p, url: '' }))
    try {
      const res = await importPageFromURL(url.trim())
      setTitle(res.title || title)
      setContent(res.content || '')
      toast.success(t('importDialog.fetchedToast'))
      return false // stay open so the user can review and save
    } catch (err: unknown) {
      console.warn(err)
      const mapped = mapApiError(err, t('importDialog.fetchError'))
      toast.error(mapped.message)
      return false
    } finally {
      setFetching(false)
    }
  }

  const handleSummarize = async (): Promise<boolean> => {
    if (!contentLoaded) {
      toast.error(t('importDialog.noContent'))
      return false
    }
    if (!llmConfigured) {
      toast.error(t('importDialog.llmNotConfigured'))
      return false
    }
    setSummarizing(true)
    try {
      const res = await summarizeContent(content)
      setContent(res.content || content)
      toast.success(t('importDialog.summarizeToast'))
      return false
    } catch (err: unknown) {
      console.warn(err)
      const mapped = mapApiError(err, t('importDialog.summarizeError'))
      toast.error(
        mapped.code === 'page_llm_not_configured'
          ? t('importDialog.llmNotConfigured')
          : mapped.message,
      )
      return false
    } finally {
      setSummarizing(false)
    }
  }

  const handleCreate = async (): Promise<boolean> => {
    if (!title.trim()) {
      setFieldErrors((p) => ({ ...p, title: t('importDialog.titleRequired') }))
      return false
    }
    if (!contentLoaded) {
      toast.error(t('importDialog.noContent'))
      return false
    }
    setSaving(true)
    setFieldErrors({})
    try {
      const slug = await suggestSlug(parentId || 'root', title.trim())
      if (!slug) {
        toast.error(t('importDialog.slugFailed'))
        return false
      }
      // The server creates the page and downloads the referenced images as
      // local attachments before storing the content.
      await importPageFromURLCreate({
        url: url.trim(),
        parentId: parentId || '',
        title: title.trim(),
        slug,
        content,
      })
      toast.success(t('importDialog.createdToast'))
      await reloadTree()
      useDialogsStore.getState().closeDialog()
      const parentPath = parentId ? getPathById(parentId) || '' : ''
      const fullPath = parentPath !== '' ? `${parentPath}/${slug}` : slug
      navigate(buildEditUrl(fullPath))
      return true
    } catch (err: unknown) {
      console.warn(err)
      handleFieldErrors(
        err,
        setFieldErrors,
        t('importDialog.createErrorFallback'),
      )
      return false
    } finally {
      setSaving(false)
    }
  }

  const handleConfirm = async (actionType: string): Promise<boolean> => {
    if (actionType === 'fetch') return await handleFetch()
    if (actionType === 'summarize') return await handleSummarize()
    if (actionType === 'create') return await handleCreate()
    return false
  }

  const buttons: BaseDialogConfirmButton[] = [
    {
      label: t('importDialog.fetch'),
      actionType: 'fetch',
      loading: fetching,
      disabled: fetching || summarizing || saving || !url.trim(),
      variant: 'secondary',
    },
    {
      label: t('importDialog.summarize'),
      actionType: 'summarize',
      loading: summarizing,
      disabled: fetching || summarizing || saving || !contentLoaded || !llmConfigured,
      variant: 'secondary',
    },
    {
      label: t('importDialog.create'),
      actionType: 'create',
      loading: saving,
      disabled: saving || !contentLoaded,
      variant: 'default',
    },
  ]

  return (
    <BaseDialog
      dialogTitle={t('importDialog.titleImport')}
      dialogDescription={t('importDialog.descImport')}
      dialogType={DIALOG_IMPORT_URL}
      onClose={() => {
        resetForm()
        return true
      }}
      onConfirm={handleConfirm}
      testidPrefix="import-url-dialog"
      cancelButton={{
        label: t('common.cancel'),
        variant: 'outline',
        disabled: fetching || summarizing || saving,
        autoFocus: false,
      }}
      buttons={buttons}
    >
      <div className="page-dialog__fields">
        <FormInput
          autoFocus={true}
          label={t('importDialog.urlLabel')}
          value={url}
          onChange={(val) => {
            setURL(val)
            setFieldErrors((p) => ({ ...p, url: '' }))
          }}
          testid="import-url-input"
          placeholder="https://example.com/article"
          error={fieldErrors.url}
        />
        <div className="page-dialog__field">
          <label className="form-label">{t('importDialog.parentLabel')}</label>
          <Select value={parentId} onValueChange={(val) => setParentId(val)}>
            <SelectTrigger data-testid="import-url-parent-select">
              <SelectValue placeholder={t('importDialog.parentPlaceholder')} />
            </SelectTrigger>
            <SelectContent>
              {parentOptions.map((opt) => (
                <SelectItem key={opt.value || 'root'} value={opt.value}>
                  {opt.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <FormInput
          label={t('importDialog.titleLabel')}
          value={title}
          onChange={(val) => {
            setTitle(val)
            setFieldErrors((p) => ({ ...p, title: '' }))
          }}
          testid="import-url-title-input"
          placeholder={t('importDialog.titlePlaceholder')}
          error={fieldErrors.title}
        />
        <div className="page-dialog__field">
          <label className="form-label">{t('importDialog.contentLabel')}</label>
          <Textarea
            data-testid="import-url-content-input"
            value={content}
            onChange={(e) => setContent(e.target.value)}
            rows={14}
            placeholder={t('importDialog.contentPlaceholder')}
            className="font-mono text-sm"
          />
        </div>
      </div>
    </BaseDialog>
  )
}
