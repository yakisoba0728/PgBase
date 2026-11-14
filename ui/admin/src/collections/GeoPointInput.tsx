import { useId } from "react";
import { RiMapPinLine } from "@remixicon/react";
import type { CollectionField } from "../api/collections";
import { useT } from "../i18n";
import { Input } from "../components/Field";

interface GeoPointInputProps {
  field: CollectionField;
  /** [lon, lat] as typed. */
  value: string[];
  onChange: (value: string[]) => void;
  error?: string;
  readOnly: boolean;
}

/** Longitude and latitude side by side, with a link to look the point up on a map. */
export function GeoPointInput({ field, value, onChange, error, readOnly }: GeoPointInputProps) {
  const t = useT();
  const id = useId();
  const [lon = "", lat = ""] = value;
  const valid = lon !== "" && lat !== "" && Number.isFinite(Number(lon)) && Number.isFinite(Number(lat));
  const map = `https://www.openstreetmap.org/?mlat=${encodeURIComponent(lat)}&mlon=${encodeURIComponent(lon)}#map=12/${encodeURIComponent(lat)}/${encodeURIComponent(lon)}`;
  return (
    <div className="field-wrap">
      <div role="group" aria-labelledby={`${id}-label`} className="field geo" data-invalid={error ? "" : undefined} aria-describedby={error || field.help ? `${id}-note` : undefined}>
        <span id={`${id}-label`} className="field-label">
          {field.name}
          {field.required && <span className="field-required" aria-hidden>*</span>}
          <span className="mono">{field.type}</span>
        </span>
        <div className="geo-inputs">
          <label>
            <span className="txt-hint">{t("geo.lon")}</span>
            <Input mono inputMode="decimal" autoComplete="off" value={lon} disabled={readOnly} placeholder="-180 … 180" invalid={Boolean(error)} onChange={(event) => onChange([event.target.value, lat])} />
          </label>
          <label>
            <span className="txt-hint">{t("geo.lat")}</span>
            <Input mono inputMode="decimal" autoComplete="off" value={lat} disabled={readOnly} placeholder="-90 … 90" invalid={Boolean(error)} onChange={(event) => onChange([lon, event.target.value])} />
          </label>
          {valid && (
            <a className="icon-btn" href={map} target="_blank" rel="noreferrer noopener" aria-label={t("geo.openMap")} title={t("geo.openMap")}>
              <RiMapPinLine size={16} aria-hidden />
            </a>
          )}
        </div>
      </div>
      {(error || field.help) && (
        <p id={`${id}-note`} className={error ? "field-error" : "field-hint"} role={error ? "alert" : undefined}>
          {error ?? field.help}
        </p>
      )}
    </div>
  );
}
