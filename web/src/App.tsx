import { Outlet } from 'react-router-dom'
import { useEffect } from 'react'
import { useAppStore } from './store'

export default function App() {
  const connectSSE = useAppStore(s => s.connectSSE)

  useEffect(() => {
    connectSSE()
  }, [connectSSE])

  return (
    <div className="h-screen flex flex-col bg-slate-50 text-slate-800 antialiased">
      <Outlet />
    </div>
  )
}
