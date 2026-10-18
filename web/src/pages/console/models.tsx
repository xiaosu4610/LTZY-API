import { useTranslation } from 'react-i18next';
import { useAppStore } from '@/stores/app';
import { ModelPlaza } from '@/components/model-plaza';
import { PageHeader } from '@/components/ui/page';

// 后台的模型广场：数据与首页广场共用同一个组件，
// 区别只有数据源（这里按登录用户所属分组，首页固定看 default）。
// 复用组件是为了让「广场上看到的价格」在首页和后台永远一致，不会各算一套。
export default function ModelsPage() {
  const { t } = useTranslation();
  const user = useAppStore((s) => s.user);

  return (
    <div>
      <PageHeader title={t('models.title')} desc={t('models.subtitle', { group: user?.group ?? 'default' })} />
      <ModelPlaza scope="me" showToolbar={false} />
    </div>
  );
}
