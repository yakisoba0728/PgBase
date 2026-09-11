import { useEffect, useRef } from "react"; import type { AdminExtensionScreen } from "./admin-extensions";
export function ExtensionScreen({ screen }: { screen: AdminExtensionScreen }) { const container = useRef<HTMLDivElement>(null); useEffect(() => screen.mount(container.current!), [screen]); return <section><h2>{screen.label}</h2><div aria-live="polite" ref={container} /></section>; }
