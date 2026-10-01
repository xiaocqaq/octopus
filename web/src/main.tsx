import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { queryClient } from '@/api/client';
import { AppContainer } from '@/components/app';
import { TooltipProvider } from '@/components/ui/tooltip';
import { Toaster } from '@/components/ui/sonner';
import { LocaleProvider } from '@/provider/locale';
import { ThemeProvider } from '@/provider/theme';
import './globals.css';

createRoot(document.getElementById('root')!).render(
  <ThemeProvider>
    <QueryClientProvider client={queryClient}>
      <LocaleProvider>
        <TooltipProvider>
          <AppContainer />
          <Toaster
            position="top-left"
            toastOptions={{
              classNames: {
                success: '[&_[data-icon]]:text-primary',
                error: '[&_[data-icon]]:text-destructive',
                warning: '[&_[data-icon]]:text-destructive/70',
              },
            }}
          />
        </TooltipProvider>
      </LocaleProvider>
    </QueryClientProvider>
  </ThemeProvider>,
);

// 生产环境在页面加载完成后注册当前应用目录作用域的 Service Worker。
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    void navigator.serviceWorker.register('./sw.js', { scope: './' }).catch((error: unknown) => {
      console.error('Service Worker registration failed', error);
    });
  });
}
