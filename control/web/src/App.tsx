import { BrowserRouter, Route, Routes } from 'react-router-dom';
import Layout from './Layout';
import { LiveProvider } from './live';
import Chat from './pages/Chat';
import EvolutionDetail from './pages/EvolutionDetail';
import Evolutions from './pages/Evolutions';
import ExtensionPage from './pages/ExtensionPage';
import Generations from './pages/Generations';
import Knowledge from './pages/Knowledge';
import Logs from './pages/Logs';
import NotFound from './pages/NotFound';
import Health from './pages/Health';
import Spending from './pages/Spending';
import Routines from './pages/Routines';
import Settings from './pages/Settings';
import Skills from './pages/Skills';

export default function App() {
  return (
    <BrowserRouter basename="/_seed">
      <LiveProvider>
        <Routes>
          <Route element={<Layout />}>
            <Route index element={<Chat />} />
            <Route path="evolutions" element={<Evolutions />} />
            <Route path="evolutions/:id" element={<EvolutionDetail />} />
            <Route path="generations" element={<Generations />} />
            <Route path="routines" element={<Routines />} />
            <Route path="health" element={<Health />} />
            <Route path="spending" element={<Spending />} />
            <Route path="skills" element={<Skills />} />
            <Route path="skills/:name" element={<Skills />} />
            <Route path="knowledge" element={<Knowledge />} />
            <Route path="logs" element={<Logs />} />
            <Route path="settings" element={<Settings />} />
            <Route path="x/:id" element={<ExtensionPage />} />
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </LiveProvider>
    </BrowserRouter>
  );
}
