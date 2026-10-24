import type { AdminSettings } from "../backend/ports";

// Before authentication, appearance belongs to the current computer rather
// than the administrator settings left by a previous session.
export function resetWorkspaceTheme() {
  applyWorkspaceTheme({ theme: "system", accent: "", branding: "" });
}

// The legacy default opts into the theme palette. Other saved accent colors
// remain explicit overrides; loading and saving use the same presentation.
export function applyWorkspaceTheme(application: AdminSettings["application"]) {
  document.documentElement.dataset.theme = application.theme;
  const themeAccent = ["", "#1d4ed8", "#ded6ee"].includes(application.accent.trim().toLowerCase());
  document.documentElement.style.setProperty("--accent", themeAccent ? "var(--default-accent)" : application.accent);
  document.documentElement.dataset.accent = themeAccent ? "theme" : "custom";
  if (themeAccent) document.documentElement.style.removeProperty("--action-text");
  else {
    const hex = application.accent.replace("#", "");
    const channels = [0, 2, 4].map(offset => parseInt(hex.slice(offset, offset + 2), 16) / 255).map(value => value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4);
    const luminance = channels[0] * .2126 + channels[1] * .7152 + channels[2] * .0722;
    document.documentElement.style.setProperty("--action-text", luminance > .179 ? "#000" : "#fff");
  }
  document.title = application.branding || "pgBase administration";
}
