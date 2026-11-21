import "@testing-library/jest-dom/vitest";
import { loadLanguage } from "../i18n";

// The app awaits its dictionary before the first render (main.tsx); tests render directly, so both are loaded up front
// and setLanguage switches synchronously as it does after a language has been used once.
await Promise.all([loadLanguage("en"), loadLanguage("ko")]);

// jsdom lacks the modal dialog API; this mirrors the parts the components use.
if (!HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
    this.open = false;
    this.dispatchEvent(new Event("close"));
  };
}
