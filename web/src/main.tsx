import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { Toaster, toast } from 'sonner';
import { router, useBootstrap } from './router';
import { setApiErrorHandler } from './lib/api';
import './i18n';
import './styles/globals.css';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 15_000,
    },
  },
});

// 统一错误提示接到 toast，业务代码里不用重复写
setApiErrorHandler((message) => toast.error(message));

function Bootstrap() {
  useBootstrap();
  return <RouterProvider router={router} />;
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <Bootstrap />
      <Toaster
        position="top-right"
        theme="dark"
        toastOptions={{
          style: {
            background: 'var(--ltzy-surface)',
            border: '1px solid var(--ltzy-border)',
            color: 'var(--ltzy-fg)',
          },
        }}
      />
    </QueryClientProvider>
  </StrictMode>,
);
