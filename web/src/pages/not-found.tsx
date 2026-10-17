import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';

export default function NotFoundPage() {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-screen flex-col items-center justify-center px-6 text-center">
      <div className="font-mono text-6xl font-semibold text-[var(--ltzy-primary)]/70">404</div>
      <h1 className="mt-4 text-xl font-semibold">{t('error.notFound')}</h1>
      <p className="mt-2 max-w-sm text-sm text-[var(--ltzy-muted)]">{t('error.notFoundDesc')}</p>
      <Link to="/" className="mt-6">
        <Button>{t('error.goHome')}</Button>
      </Link>
    </div>
  );
}
