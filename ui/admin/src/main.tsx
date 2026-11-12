// Self-hosted Pretendard subsets (unicode-range), with the system stack as fallback in tokens.css.
import "pretendard/dist/web/variable/pretendardvariable-dynamic-subset.css";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/buttons.css";
import "./styles/forms.css";
import "./styles/data.css";
import "./styles/overlays.css";
import "./styles/layout.css";
import "./styles/schema.css";
// Applies the stored theme and language before the first render (no inline script under the CSP).
import "./app/theme";
import "./i18n";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router";
import { QueryClientProvider } from "@tanstack/react-query";
import { registerScreen } from "./app/extensions";
import { queryClient } from "./app/queries";
import { router } from "./app/router";
import { ToastHost } from "./components/Toast";

// Defined before render; the server-injected extensions.js module runs after this one.
window.pgbaseAdmin = { registerScreen };

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <ToastHost />
    </QueryClientProvider>
  </StrictMode>,
);
