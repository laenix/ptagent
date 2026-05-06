import { Outlet } from 'react-router-dom'

export default function App() {
  return (
    <div className="h-screen flex flex-col bg-slate-50 text-slate-800 antialiased">
      <Outlet />
    </div>
  )
}
