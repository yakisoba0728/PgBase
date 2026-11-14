import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { Page } from "../components/Page";
import { AppShell } from "./AppShell";
import { setDrawer } from "./shell";
vi.mock("./AppNav", () => ({ AppNav: () => <aside className="nav"><a href="/">Navigation link</a></aside> }));
vi.mock("./shortcuts", () => ({ useShellShortcuts: () => {} }));
afterEach(() => { cleanup(); setDrawer(false); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
it("moves keyboard focus into a mobile drawer and closes from outside it with Escape", async () => {
  vi.stubGlobal("innerWidth", 600);
  const router = createMemoryRouter([{ element: <AppShell />, children: [{ path: "/", element: <Page title="Test">Content</Page> }] }]);
  render(<RouterProvider router={router} />);
  const trigger = screen.getByRole("button", { name: "Open navigation" });
  trigger.focus();
  fireEvent.click(trigger);
  expect(document.querySelector("main")).toHaveAttribute("inert");
  await waitFor(() => expect(screen.getByRole("link", { name: "Navigation link" })).toHaveFocus());
  fireEvent.keyDown(document, { key: "Escape" });
  expect(document.querySelector("main")).not.toHaveAttribute("inert");
  expect(trigger).toHaveFocus();
});

it("retries focus after the drawer visibility transition and cancels work on close", () => {
  vi.stubGlobal("innerWidth", 600);
  let frame!: FrameRequestCallback;
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => { frame = callback; return 1; });
  const cancel = vi.fn();
  vi.stubGlobal("cancelAnimationFrame", cancel);
  const router = createMemoryRouter([{ element: <AppShell />, children: [{ path: "/", element: <button onClick={(event) => { setDrawer(true, event.currentTarget); event.currentTarget.blur(); }}>Open navigation</button> }] }]);
  render(<RouterProvider router={router} />);
  const trigger = screen.getByRole("button", { name: "Open navigation" });
  const link = screen.getByRole("link", { name: "Navigation link" });
  const focus = link.focus.bind(link);
  let visible = false;
  vi.spyOn(link, "focus").mockImplementation(() => { if (visible) focus(); });
  trigger.focus();
  fireEvent.click(trigger);
  act(() => frame(0));
  expect(link).not.toHaveFocus();
  visible = true;
  fireEvent.transitionEnd(document.querySelector(".nav")!, { propertyName: "visibility" });
  expect(link).toHaveFocus();
  fireEvent.keyDown(document, { key: "Escape" });
  expect(cancel).toHaveBeenCalledWith(1);
  expect(trigger).toHaveFocus();
  fireEvent.transitionEnd(document.querySelector(".nav")!, { propertyName: "visibility" });
  expect(trigger).toHaveFocus();
});

it("shows the destination skeleton during lazy navigation and removes it when ready", async () => {
  const { lazy } = await import("react");
  let finish!: (module: { default: () => React.ReactNode }) => void;
  const Pending = lazy(() => new Promise<{ default: () => React.ReactNode }>((resolve) => { finish = resolve; }));
  const router = createMemoryRouter([{ element: <AppShell />, children: [
    { path: "/collections", element: <Page title="Collections">Loaded list</Page> },
    { path: "/settings", element: <Pending /> },
  ] }], { initialEntries: ["/collections"] });
  render(<RouterProvider router={router} />);
  await act(async () => { await router.navigate("/settings"); });
  expect(screen.getByRole("heading", { name: "Settings" })).toBeInTheDocument();
  expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true");
  expect(screen.getByRole("link", { name: "Navigation link" })).toBeInTheDocument();
  expect(screen.queryByText("Loaded list")).not.toBeInTheDocument();
  await act(async () => { finish({ default: () => <Page title="Settings">Settings loaded</Page> }); });
  expect(screen.getByText("Settings loaded")).toBeInTheDocument();
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
});

it("closes the mobile drawer on any navigation, not only nav link clicks", async () => {
  vi.stubGlobal("innerWidth", 600);
  const router = createMemoryRouter([{ element: <AppShell />, children: [
    { path: "/", element: <Page title="Home">Home</Page> },
    { path: "/profile", element: <Page title="Profile">Profile</Page> },
  ] }]);
  render(<RouterProvider router={router} />);
  act(() => setDrawer(true));
  expect(document.querySelector("main")).toHaveAttribute("inert");
  await act(async () => { await router.navigate("/profile"); });
  expect(document.querySelector("main")).not.toHaveAttribute("inert");
});

it("keeps the screen mounted across its own tabs and remounts it for another collection", async () => {
  let mounts = 0;
  function Screen() {
    const [id] = useState(() => ++mounts);
    return <Page title="Collection">mount {id}</Page>;
  }
  const router = createMemoryRouter([{ element: <AppShell />, children: [{ path: "/collections/:name/:tab?", element: <Screen /> }] }], { initialEntries: ["/collections/a"] });
  render(<RouterProvider router={router} />);
  expect(screen.getByText("mount 1")).toBeInTheDocument();
  await act(async () => { await router.navigate("/collections/a/schema"); });
  expect(screen.getByText("mount 1")).toBeInTheDocument();
  await act(async () => { await router.navigate("/collections/b"); });
  expect(screen.getByText("mount 2")).toBeInTheDocument();
});
