import { RiArrowDownSLine, RiArrowUpSLine, RiLayoutColumnLine } from "@remixicon/react";
import { Button, IconButton } from "../components/Button";
import { Popover } from "../components/Popover";
import { useT } from "../i18n";
import { moveColumn, orderedColumns, toggleColumn, type ColumnPrefs } from "./columns";

interface ColumnSettingsProps {
  available: string[];
  prefs: ColumnPrefs;
  onChange: (prefs: ColumnPrefs | null) => void;
}

/** Show, hide and reorder grid columns; remembered per collection on this device. */
export function ColumnSettings({ available, prefs, onChange }: ColumnSettingsProps) {
  const t = useT();
  const hidden = prefs.hidden.filter((name) => available.includes(name)).length;
  return (
    <Popover
      label={hidden ? `${t("columns.title")} · ${t("columns.hidden", { count: hidden })}` : t("columns.title")}
      icon={RiLayoutColumnLine}
      buttonClassName={hidden ? "icon-btn columns-btn-hidden" : "icon-btn"}
      className="columns-panel"
    >
      {() => {
        const order = orderedColumns(available, prefs);
        return (
          <>
            <div className="columns-head">
              <span>{t("columns.title")}</span>
              <Button size="sm" variant="transparent" onClick={() => onChange(null)}>{t("columns.reset")}</Button>
            </div>
            <ul className="columns-list">
              {order.map((name, index) => (
                <li key={name}>
                  <label className="checkbox">
                    <input type="checkbox" checked={!prefs.hidden.includes(name)} onChange={(event) => onChange(toggleColumn(prefs, name, event.target.checked))} />
                    <span className="mono">{name}</span>
                  </label>
                  <IconButton label={t("columns.up", { name })} icon={RiArrowUpSLine} disabled={index === 0} onClick={() => onChange(moveColumn(available, prefs, name, -1))} />
                  <IconButton label={t("columns.down", { name })} icon={RiArrowDownSLine} disabled={index === order.length - 1} onClick={() => onChange(moveColumn(available, prefs, name, 1))} />
                </li>
              ))}
            </ul>
          </>
        );
      }}
    </Popover>
  );
}
