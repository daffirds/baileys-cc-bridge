import { useEffect, useState } from 'react';
import { Icon } from './icons';

// The panel follows the system's light or dark mode until its toggle picks one, which this browser
// then remembers. The pick sets data-theme on <html>; styles.css does the rest.

type Theme = 'light' | 'dark';

const dark = () => matchMedia('(prefers-color-scheme: dark)');

function picked(): Theme | null {
  try {
    const t = localStorage.getItem('theme');
    return t === 'light' || t === 'dark' ? t : null;
  } catch {
    return null;
  }
}

// applyTheme sets the remembered pick before the first paint, so the page doesn't flash.
export function applyTheme() {
  const t = picked();
  if (t) document.documentElement.dataset.theme = t;
}

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(() => picked() ?? (dark().matches ? 'dark' : 'light'));

  // Without a pick, keep up with the system.
  useEffect(() => {
    const mq = dark();
    const follow = () => {
      if (!picked()) setTheme(mq.matches ? 'dark' : 'light');
    };
    mq.addEventListener('change', follow);
    return () => mq.removeEventListener('change', follow);
  }, []);

  const toggle = () => {
    const next: Theme = theme === 'dark' ? 'light' : 'dark';
    try {
      localStorage.setItem('theme', next);
    } catch {
      // Not remembered; it still switches.
    }
    const apply = () => {
      document.documentElement.dataset.theme = next;
      setTheme(next);
    };
    const doc = document as Document & { startViewTransition?: (cb: () => void) => unknown };
    if (doc.startViewTransition && !matchMedia('(prefers-reduced-motion: reduce)').matches) {
      doc.startViewTransition(apply);
    } else {
      apply();
    }
  };

  const label = theme === 'dark' ? 'Light mode' : 'Dark mode';
  return (
    <button type="button" className="icon-button theme-toggle" title={label} aria-label={label} onClick={toggle}>
      <Icon key={theme} name={theme === 'dark' ? 'sun' : 'moon'} size={18} />
    </button>
  );
}
