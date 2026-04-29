import React from 'react';
import ReactDOM from 'react-dom/client';
import { App } from './App';
import './styles.css';

const stored = localStorage.getItem('clichat.theme');
const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
const initialTheme = stored === 'dark' || stored === 'light' ? stored : prefersDark ? 'dark' : 'light';
document.documentElement.dataset.theme = initialTheme;

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
