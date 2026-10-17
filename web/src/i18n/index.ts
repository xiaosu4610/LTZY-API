import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import en from './en';
import zh from './zh';

const LANG_KEY = 'ltzy-lang';

export type Lang = 'zh' | 'en';

export function readLang(): Lang {
  const saved = localStorage.getItem(LANG_KEY);
  if (saved === 'zh' || saved === 'en') return saved;
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

void i18n.use(initReactI18next).init({
  resources: {
    zh: { translation: zh },
    en: { translation: en },
  },
  lng: readLang(),
  fallbackLng: 'zh',
  interpolation: { escapeValue: false },
});

export function changeLang(lang: Lang) {
  localStorage.setItem(LANG_KEY, lang);
  void i18n.changeLanguage(lang);
  document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en';
}

document.documentElement.lang = readLang() === 'zh' ? 'zh-CN' : 'en';

export default i18n;
