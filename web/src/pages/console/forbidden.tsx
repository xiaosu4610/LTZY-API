import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';

// 403 页面：无对应权限点时的落点（由路由守卫 beforeLoad 重定向而来）
export default function ForbiddenPage() {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center px-6 text-center">
      <div className="font-mono text-6xl font-semibold text-[var(--ltzy-primary)]/70">403</div>
      <h1 className="mt-4 text-xl font-semibold">{t('error.forbidden')}</h1>
      <p className="mt-2 max-w-sm text-sm text-[var(--ltzy-muted)]">{t('error.forbiddenDesc')}</p>
      <Link to="/console" className="mt-6">
        <Button>{t('error.backToConsole')}</Button>
      </Link>
    </div>
  );
}
