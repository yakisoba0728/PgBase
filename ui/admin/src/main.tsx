import { createRoot } from "react-dom/client";
import { App } from "./app";
import { installAdminExtensionAPI } from "./extensions/admin-extensions";
import "./styles.css";

installAdminExtensionAPI();
createRoot(document.getElementById("root")!).render(<App />);
