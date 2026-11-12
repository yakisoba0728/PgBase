import { useLocale, type Locale } from ".";

export function LanguagePicker() {
  const { locale, setLocale, t } = useLocale();
  return <label className="language-picker"><span>{t("Language")}</span><select className="language-select" aria-label={t("Language")} value={locale} onChange={event => setLocale(event.target.value as Locale)}><option value="ko">한국어</option><option value="en">English</option></select></label>;
}
