import { useAppStore } from '../store'

export default function Toast() {
  const toast = useAppStore(s => s.toast)

  if (!toast.show) return null

  const isError = toast.type === 'error'

  return (
    <div className="fixed bottom-4 right-4 z-[9999] toast-enter">
      <div className={`rounded-xl px-4 py-2.5 text-sm shadow-lg border backdrop-blur ${
        isError
          ? 'bg-rose-50/95 border-rose-200 text-rose-700'
          : 'bg-white/95 border-slate-200 text-slate-700'
      }`}>
        <div className="flex items-center gap-2">
          {isError ? (
            <svg className="w-4 h-4 text-rose-500 shrink-0" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10" /><path d="m15 9-6 6" /><path d="m9 9 6 6" /></svg>
          ) : (
            <svg className="w-4 h-4 text-emerald-500 shrink-0" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" /></svg>
          )}
          {toast.message}
        </div>
      </div>
    </div>
  )
}
