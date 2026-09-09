import { useEffect, useRef, useState } from "react";
import land from "../assets/world-land-paths.json";

interface Props {
  id: string; label: string; value: string; disabled: boolean;
  describedBy?: string; invalid?: boolean; onChange(value: string): void;
}
function coordinates(value: string): { lon: string; lat: string } {
  try { const point = JSON.parse(value); return { lon: String(point.lon ?? ""), lat: String(point.lat ?? "") }; }
  catch { return { lon: "", lat: "" }; }
}
const clamp = (value: number, minimum: number, maximum: number) => Math.min(maximum, Math.max(minimum, value));

export function GeoPointField({ id, label, value, disabled, describedBy, invalid, onChange }: Props) {
  const [point, setPoint] = useState(() => coordinates(value));
  const [zoom, setZoom] = useState(1);
  const emitted = useRef(value);
  useEffect(() => { if (value !== emitted.current) { setPoint(coordinates(value)); emitted.current = value; } }, [value]);
  const lon = Number(point.lon), lat = Number(point.lat);
  const valid = point.lon !== "" && point.lat !== "" && Number.isFinite(lon) && Number.isFinite(lat) && Math.abs(lon) <= 180 && Math.abs(lat) <= 90;
  const width = 360 / zoom, height = 180 / zoom;
  const x = clamp((valid ? lon : 0) + 180 - width / 2, 0, 360 - width);
  const y = clamp(90 - (valid ? lat : 0) - height / 2, 0, 180 - height);
  function change(next: typeof point) {
    setPoint(next);
    // Preserve incomplete numeric input as invalid JSON values, never as zero.
    emitted.current = JSON.stringify({ lon: next.lon.trim() === "" ? null : Number(next.lon), lat: next.lat.trim() === "" ? null : Number(next.lat) });
    onChange(emitted.current);
  }
  function select(longitude: number, latitude: number) {
    change({ lon: String(Number(clamp(longitude, -180, 180).toFixed(6))), lat: String(Number(clamp(latitude, -90, 90).toFixed(6))) });
  }
  return <fieldset className="geo-point-field" disabled={disabled} aria-describedby={describedBy}>
    <legend>{label} coordinates</legend>
    <label htmlFor={id}>Longitude<input id={id} aria-label={`${label} longitude`} aria-invalid={invalid} type="number" min={-180} max={180} step="any" value={point.lon} onChange={event => change({ ...point, lon: event.target.value })} /></label>
    <label>Latitude<input aria-label={`${label} latitude`} aria-invalid={invalid} type="number" min={-90} max={90} step="any" value={point.lat} onChange={event => change({ ...point, lat: event.target.value })} /></label>
    <p id={`${id}-map-help`}>Click the map to set coordinates. Arrow keys move the point; hold Shift for smaller steps. Enter precise coordinates above.</p>
    <svg className="coordinate-map" role="group" aria-label={`${label} coordinate map`} aria-describedby={`${id}-map-help`} aria-disabled={disabled} tabIndex={disabled ? -1 : 0} viewBox={`${x} ${y} ${width} ${height}`} preserveAspectRatio="none" onClick={event => {
      if (disabled) return;
      const box = event.currentTarget.getBoundingClientRect();
      if (box.width && box.height) select(x + (event.clientX - box.left) / box.width * width - 180, 90 - y - (event.clientY - box.top) / box.height * height);
    }} onKeyDown={event => {
      if (disabled || !["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(event.key)) return;
      event.preventDefault();
      const step = (event.shiftKey ? 0.1 : 1) / zoom;
      select((valid ? lon : 0) + (event.key === "ArrowLeft" ? -step : event.key === "ArrowRight" ? step : 0), (valid ? lat : 0) + (event.key === "ArrowDown" ? -step : event.key === "ArrowUp" ? step : 0));
    }}>
      <rect x="0" y="0" width="360" height="180" fill="#d9edf6" />
      {land.map((d, index) => <path key={index} d={d} fill="#d4dfc7" stroke="#889e79" strokeWidth={0.3 / zoom} fillRule="evenodd" />)}
      {Array.from({ length: 11 }, (_, index) => <path key={`lon${index}`} d={`M${30 * (index + 1)},0v180`} stroke="#8da8b3" strokeWidth={0.2 / zoom} />)}
      {Array.from({ length: 5 }, (_, index) => <path key={`lat${index}`} d={`M0,${30 * (index + 1)}h360`} stroke="#8da8b3" strokeWidth={0.2 / zoom} />)}
      {valid && <circle cx={lon + 180} cy={90 - lat} r={2.5 / zoom} fill="#d83243" stroke="white" strokeWidth={0.8 / zoom} />}
    </svg>
    <button type="button" disabled={disabled || zoom >= 64} onClick={() => setZoom(zoom * 2)}>Zoom in</button>
    <button type="button" disabled={disabled || zoom <= 1} onClick={() => setZoom(zoom / 2)}>Zoom out</button>
    <button type="button" disabled={disabled} onClick={() => setZoom(1)}>World view</button>
    <small>Made with Natural Earth. The offline map shows approximate coastlines.</small>
  </fieldset>;
}
