import { useEffect, useState, type ComponentProps } from "react";
import { Button } from "./ui";

export function PasswordInput({ id, value, disabled, ...props }: Omit<ComponentProps<"input">, "type"> & { id: string }) {
  const [revealed, setRevealed] = useState(false);
  useEffect(() => { if (value === "") setRevealed(false); }, [value]);
  return <div className="password-input">
    <input {...props} disabled={disabled} id={id} type={revealed ? "text" : "password"} value={value} />
    <Button aria-controls={id} aria-label={revealed ? "Hide password" : "Show password"} aria-pressed={revealed} className="password-toggle" disabled={disabled} onClick={() => setRevealed(!revealed)} variant="ghost">{revealed ? "Hide" : "Show"}</Button>
  </div>;
}
