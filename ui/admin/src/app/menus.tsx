import { useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { RiComputerLine, RiLogoutBoxRLine, RiMoonLine, RiSunLine, RiTranslate2, RiUser3Line, RiUserSettingsLine } from "@remixicon/react";
import { Dropdown, DropdownItem } from "../components/Popover";
import { languages, setLanguage, useLanguage, useT } from "../i18n";
import { sessionQuery } from "./queries";
import { useSignOut } from "./signout";
import { setTheme, themeModes, useTheme } from "./theme";

const themeIcons = { light: RiSunLine, dark: RiMoonLine, auto: RiComputerLine };
// Language names stay in their own language so either can be found.
const languageNames = { ko: "한국어", en: "English" };

export function ThemeMenu() {
  const t = useT();
  const mode = useTheme();
  return (
    <Dropdown label={t("menu.theme")} heading={t("menu.theme")} icon={themeIcons[mode]} align="start">
      {themeModes.map((option) => (
        <DropdownItem key={option} icon={themeIcons[option]} checked={option === mode} onSelect={() => setTheme(option)}>
          {t(`theme.${option}`)}
        </DropdownItem>
      ))}
    </Dropdown>
  );
}

export function LanguageMenu() {
  const t = useT();
  const language = useLanguage();
  return (
    <Dropdown label={t("menu.language")} heading={t("menu.language")} icon={RiTranslate2} align="start">
      {languages.map((option) => (
        <DropdownItem key={option} checked={option === language} onSelect={() => setLanguage(option)}>
          <span lang={option}>{languageNames[option]}</span>
        </DropdownItem>
      ))}
    </Dropdown>
  );
}

export function AccountMenu() {
  const t = useT();
  const navigate = useNavigate();
  const { data: session } = useQuery(sessionQuery);
  const signOut = useSignOut();
  return (
    <Dropdown label={t("menu.account")} heading={session?.administrator.email} text={session?.administrator.email} icon={RiUser3Line} align="start">
      <DropdownItem icon={RiUserSettingsLine} onSelect={() => navigate("/profile")}>
        {t("account.profile")}
      </DropdownItem>
      <DropdownItem icon={RiLogoutBoxRLine} onSelect={() => signOut()}>
        {t("account.signOut")}
      </DropdownItem>
    </Dropdown>
  );
}
