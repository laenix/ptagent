import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import cytoscape from 'cytoscape'
import dagre from 'cytoscape-dagre'
import klay from 'cytoscape-klay'
import elk from 'cytoscape-elk'
import App from './App'
import ProjectList from './pages/ProjectList'
import ProjectDetail from './pages/ProjectDetail'
import Modals from './components/Modals'
import DispatcherModal from './components/DispatcherModal'
import Toast from './components/Toast'
import CTFdPanel from './components/CTFdPanel'
import AgentChat from './components/AgentChat'
import './index.css'

// Register Cytoscape layout extensions
cytoscape.use(dagre)
cytoscape.use(klay)
cytoscape.use(elk)

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<App />}>
          <Route index element={<ProjectList />} />
          <Route path="projects/:id" element={<ProjectDetail />} />
        </Route>
      </Routes>
      <Modals />
      <DispatcherModal />
      <Toast />
      <CTFdPanel />
      <AgentChat />
    </BrowserRouter>
  </React.StrictMode>,
)
