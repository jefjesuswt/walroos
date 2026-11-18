import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { applyTheme, savedPref, watchSystemMode } from './theme';
import { ToastProvider } from './ui';
import './styles.css';

const pref = savedPref();
applyTheme(pref.id, pref.mode);
watchSystemMode();

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ToastProvider><App /></ToastProvider>
  </StrictMode>,
);
