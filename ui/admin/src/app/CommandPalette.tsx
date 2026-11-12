import { useEffect, useMemo, useState, type KeyboardEvent } from "react";
import { useLocation, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  RiAddLine,
  RiArrowRightSLine,
  RiComputerLine,
  RiDatabase2Line,
  RiEyeLine,
  RiHistoryLine,
  RiKeyboardLine,
  RiLogoutBoxRLine,
  RiMoonLine,
  RiSearchLine,
  RiSunLine,
  RiTableLine,
  RiTranslate2,
  RiUser3Line,
  type RemixiconComponentType,
} from "@remixicon/react";
import { collectionsQuery } from "../collections/queries";
import { Button } from "../components/Button";
import { TaskDialog, useModal } from "../components/Dialog";
import { languages, setLanguage, useT, type MessageKey } from "../i18n";
import { keyLabel, registerShortcut, useShortcut } from "./keys";
import { navGroups, screenTabs } from "./navigation";
import { rankItems, type PaletteItem } from "./palette";
import { collectionPath, useRecentCollections } from "./recent";
import { setHelp, setPalette, togglePalette, useHelp, usePalette } from "./shell";
import { useSignOut } from "./signout";
import { setTheme, themeModes } from "./theme";

const languageNames = { ko: "한국어", en: "English" };
const typeIcons = { base: RiTableLine, auth: RiUser3Line, view: RiEyeLine };
const themeIcons = { light: RiSunLine, dark: RiMoonLine, auto: RiComputerLine };

type Item = PaletteItem & { icon: RemixiconComponentType; hint?: string };

/** Every place and command the palette offers, built from the navigation's own lists. */
function useItems(): Item[] {
  const t = useT();
  const navigate = useNavigate();
  const signOut = useSignOut();
  const { pathname } = useLocation();
  const collections = useQuery(collectionsQuery).data ?? [];
  const recent = useRecentCollections(collections.map((collection) => collection.name));
  const current = /^\/collections\/([^/]+)/.exec(pathname)?.[1];
  const currentName = current && decodeURIComponent(current);
  const currentCollection = collections.find((collection) => collection.name === currentName);
  const go = (path: string) => () => void navigate(path);
  return [
    ...recent.map((name): Item => ({ id: `recent:${name}`, group: "recent", label: name, icon: RiHistoryLine, run: go(collectionPath(name)) })),
    ...collections.map((collection): Item => ({
      id: `collection:${collection.name}`,
      group: "collections",
      label: collection.name,
      hint: t(`collections.type.${collection.type ?? "base"}`),
      icon: typeIcons[collection.type ?? "base"],
      run: go(collectionPath(collection.name)),
    })),
    { id: "screen:collections", group: "screens", label: t("nav.collections"), keywords: "/collections", icon: RiDatabase2Line, run: go("/collections") },
    ...navGroups.flatMap((group) =>
      group.items.map((entry): Item => ({ id: `screen:${entry.path}`, group: "screens", label: t(entry.label), hint: t(group.label), keywords: `/${entry.path}`, icon: entry.icon, run: go(`/${entry.path}`) })),
    ),
    ...Object.values(screenTabs).flatMap((tabs) =>
      tabs.slice(1).map((tab): Item => ({ id: `screen:${tab.path}`, group: "screens", label: t(tab.label), hint: tab.path, keywords: tab.path, icon: RiArrowRightSLine, run: go(tab.path) })),
    ),
    { id: "screen:profile", group: "screens", label: t("account.profile"), keywords: "/profile", icon: RiUser3Line, run: go("/profile") },
    { id: "action:new-collection", group: "actions", label: t("palette.newCollection"), icon: RiAddLine, run: go("/new-collection") },
    ...(currentCollection && currentCollection.type !== "view"
      ? [{ id: "action:new-record", group: "actions", label: t("palette.newRecord", { name: currentCollection.name }), icon: RiAddLine, run: go(`/collections/${encodeURIComponent(currentCollection.name)}?new=1`) } satisfies Item]
      : []),
    ...themeModes.map((mode): Item => ({ id: `action:theme:${mode}`, group: "actions", label: t("palette.theme", { mode: t(`theme.${mode}`) }), icon: themeIcons[mode], run: () => setTheme(mode) })),
    ...languages.map((language): Item => ({ id: `action:language:${language}`, group: "actions", label: t("palette.language", { name: languageNames[language] }), keywords: "language 언어", icon: RiTranslate2, run: () => setLanguage(language) })),
    { id: "action:shortcuts", group: "actions", label: t("palette.shortcuts"), keywords: "keyboard ?", icon: RiKeyboardLine, run: () => setHelp(true) },
    { id: "action:sign-out", group: "actions", label: t("palette.signOut"), keywords: "logout log out", icon: RiLogoutBoxRLine, run: signOut },
  ];
}

/** Cmd/Ctrl+K: type to jump to any collection or screen, or run a command; arrows move, Enter runs, Esc closes. */
export function CommandPalette() {
  const open = usePalette();
  const close = () => setPalette(false);
  const modal = useModal(open, close);
  return (
    <dialog className="palette" {...modal} aria-labelledby="palette-title">
      {open && <PaletteBody onClose={close} />}
    </dialog>
  );
}

function PaletteBody({ onClose }: { onClose: () => void }) {
  const t = useT();
  const items = useItems();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const shown = useMemo(() => rankItems(items, query), [items, query]);
  const index = Math.min(active, shown.length - 1);

  const run = (item: Item | undefined) => {
    if (!item) return;
    // Closed first, so a confirmation the navigation may raise (unsaved changes) is not stacked under it.
    onClose();
    item.run();
  };
  const move = (step: number) => {
    if (!shown.length) return;
    const next = (index + step + shown.length) % shown.length;
    setActive(next);
    document.getElementById(`palette-${next}`)?.scrollIntoView({ block: "nearest" });
  };
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.nativeEvent.isComposing) return;
    const step = { ArrowDown: 1, ArrowUp: -1 }[event.key];
    if (step) {
      event.preventDefault();
      move(step);
    } else if (event.key === "Enter") {
      event.preventDefault();
      run(shown[index] as Item);
    }
  };

  return (
    <>
      <h2 id="palette-title" className="visually-hidden">{t("palette.title")}</h2>
      <div className="palette-search">
        <RiSearchLine size={16} aria-hidden />
        <input
          autoFocus
          role="combobox"
          aria-expanded
          aria-controls="palette-list"
          aria-activedescendant={shown.length ? `palette-${index}` : undefined}
          aria-label={t("palette.title")}
          placeholder={t("palette.placeholder")}
          spellCheck={false}
          autoComplete="off"
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setActive(0);
          }}
          onKeyDown={onKeyDown}
        />
        <kbd>Esc</kbd>
      </div>
      <ul id="palette-list" role="listbox" aria-label={t("palette.title")} className="palette-list">
        {shown.map((item, position) => {
          const entry = item as Item;
          const heading = position === 0 || shown[position - 1].group !== item.group;
          const Icon = entry.icon;
          return (
            <li key={item.id} role="presentation">
              {heading && <div className="palette-group" aria-hidden>{t(`palette.group.${item.group}` as MessageKey)}</div>}
              <div
                id={`palette-${position}`}
                role="option"
                aria-selected={position === index}
                className="palette-item"
                onMouseMove={() => position !== index && setActive(position)}
                onClick={() => run(entry)}
              >
                <Icon size={16} aria-hidden />
                <span className="palette-label">{item.label}</span>
                {entry.hint && <span className="palette-hint">{entry.hint}</span>}
              </div>
            </li>
          );
        })}
        {shown.length === 0 && <li className="palette-empty" role="presentation">{t("palette.empty")}</li>}
      </ul>
    </>
  );
}

// The shell's shortcuts; also the list the help overlay shows.
const general: { keys: string; label: MessageKey }[] = [
  { keys: "mod+k", label: "shortcuts.palette" },
  { keys: "?", label: "shortcuts.help" },
  { keys: "/", label: "shortcuts.filter" },
  { keys: "n", label: "shortcuts.new" },
  { keys: "Escape", label: "shortcuts.close" },
  { keys: "mod+s", label: "shortcuts.save" },
];
const gotoEntries = [{ key: "c", path: "/collections", label: "nav.collections" as MessageKey }, ...navGroups.flatMap((group) => group.items.map((entry) => ({ key: entry.key, path: `/${entry.path}`, label: entry.label })))];

/** Registers the shell shortcuts (palette, help, filter, new, go-to); Esc and save belong to panes and save bars. */
export function useShellShortcuts(): void {
  const navigate = useNavigate();
  useShortcut("mod+k", togglePalette, { inFields: true });
  useShortcut("?", () => setHelp(true));
  useShortcut("/", () => {
    const input = document.querySelector<HTMLInputElement>("main [data-filter]");
    if (!input) return false;
    input.focus();
    input.select();
  });
  useShortcut("n", () => {
    const button = document.querySelector<HTMLElement>("main [data-new]");
    if (!button || (button as HTMLButtonElement).disabled) return false;
    button.click();
  });
  useEffect(() => {
    const cleanups = gotoEntries.map((entry) => registerShortcut({ keys: `g ${entry.key}`, run: () => void navigate(entry.path) }));
    return () => cleanups.forEach((cleanup) => cleanup());
  }, [navigate]);
}

function Keys({ keys }: { keys: string }) {
  const t = useT();
  const steps = keyLabel(keys);
  return (
    <span className="shortcut-keys">
      {steps.map((step, position) => (
        <span key={position}>
          {position > 0 && <span className="txt-hint"> {t("shortcuts.then")} </span>}
          <kbd>{step}</kbd>
        </span>
      ))}
    </span>
  );
}

/** "?": every shortcut, grouped. */
export function ShortcutsHelp() {
  const t = useT();
  const open = useHelp();
  return (
    <TaskDialog open={open} title={t("shortcuts.title")} onClose={() => setHelp(false)}>
      <div className="shortcuts">
        <section>
          <h3>{t("shortcuts.group.general")}</h3>
          <dl>
            {general.map((entry) => (
              <div key={entry.keys}>
                <dt>{t(entry.label)}</dt>
                <dd><Keys keys={entry.keys} /></dd>
              </div>
            ))}
          </dl>
        </section>
        <section>
          <h3>{t("shortcuts.group.navigation")}</h3>
          <dl>
            {gotoEntries.map((entry) => (
              <div key={entry.key}>
                <dt>{t(entry.label)}</dt>
                <dd><Keys keys={`g ${entry.key}`} /></dd>
              </div>
            ))}
          </dl>
        </section>
      </div>
      <div className="confirm-actions">
        <Button variant="secondary" onClick={() => setHelp(false)}>{t("common.close")}</Button>
      </div>
    </TaskDialog>
  );
}
