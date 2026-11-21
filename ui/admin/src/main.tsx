// Self-hosted Pretendard subsets (unicode-range), with the system stack as fallback in tokens.css.
import "pretendard/dist/web/variable/pretendardvariable-dynamic-subset.css";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/buttons.css";
import "./styles/forms.css";
import "./styles/data.css";
import "./styles/overlays.css";
import "./styles/kit.css";
import "./styles/widgets.css";
import "./styles/layout.css";
import "./styles/palette.css";
// Applies the stored theme and language before the first render (no inline script under the CSP).
import "./app/theme";
import { loadInitialLanguage } from "./i18n";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router";
import { QueryClientProvider } from "@tanstack/react-query";
import { onCsrfRejected } from "./api/http";
import { registerScreen } from "./app/extensions";
import { warmPath } from "./app/prefetch";
import { queryClient, signingOutKey, startSessionCheck } from "./app/queries";
import { router } from "./app/router";
import { currentAdministrator, followSignOut, recoverSession } from "./app/signout";
import { readStorage } from "./app/store";
import { ToastHost } from "./components/Toast";

// Defined before render; the server-injected extensions.js module runs after this one.
window.pgbaseAdmin = { registerScreen };
onCsrfRejected(recoverSession, currentAdministrator);
router.subscribe((state) => followSignOut(state.location));
// The dictionary for the stored language is fetched in parallel with the requests below, and the first render waits
// for it behind the static boot skeleton, so no untranslated key is ever painted.
const dictionary = loadInitialLanguage();
// The screen's chunk and first list start alongside the session check instead of after it. Only GETs, and a 401 before
// any session is known is ignored (queries.ts onError); a session still being ended is left to finish first.
if (!readStorage(signingOutKey)) {
  // The guard's check would otherwise start only at the first render, one dictionary fetch later. Guest screens
  // (/login, /setup, /reset-password) match a single route without the guarded layout's children.
  if (router.state.matches[0]?.route.children) startSessionCheck();
  warmPath(window.location.pathname.slice((router.basename ?? "").length), window.location.search);
}

// Should neither the chosen language nor English load, the app still renders (with keys) instead of a skeleton forever.
void dictionary
  .catch(() => undefined)
  .then(() =>
    createRoot(document.getElementById("root")!).render(
      <StrictMode>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
          <ToastHost />
        </QueryClientProvider>
      </StrictMode>,
    ),
  );
