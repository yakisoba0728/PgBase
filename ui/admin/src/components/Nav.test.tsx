import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router";
import { RiFileList3Line } from "@remixicon/react";
import { afterEach, expect, it, vi } from "vitest";
import { preloadRoute, routeModules } from "../app/routeModules";
import { NavItem } from "./Nav";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function Location() {
  return <output aria-label="location">{useLocation().pathname}</output>;
}

it("loads screen code on pointer intent and keyboard focus without navigating or requesting data", async () => {
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  const load = vi.spyOn(routeModules, "/logs");
  render(
    <MemoryRouter initialEntries={["/collections"]}>
      <NavItem to="/logs" icon={RiFileList3Line}>Logs</NavItem>
      <Location />
    </MemoryRouter>,
  );
  const link = screen.getByRole("link", { name: "Logs" });
  fireEvent.pointerEnter(link);
  await waitFor(() => expect(load).toHaveBeenCalledOnce());
  await load.mock.results[0].value;
  fireEvent.focus(link);
  await waitFor(() => expect(load).toHaveBeenCalledTimes(2));
  await load.mock.results[1].value;
  expect(fetch).not.toHaveBeenCalled();
  expect(screen.getByLabelText("location")).toHaveTextContent("/collections");
  fireEvent.click(link);
  expect(screen.getByLabelText("location")).toHaveTextContent("/logs");
});

it("preloads a collection's remembered subpath while preserving its click target", async () => {
  const load = vi.spyOn(routeModules, "/collections").mockResolvedValue({
    CollectionPage: () => <div />,
    CollectionsIndexPage: () => <div />,
  });
  const target = () => "/collections/Case%2FSensitive/schema?draft=1#fields";
  render(
    <MemoryRouter initialEntries={["/logs"]}>
      <NavItem to="/collections/Case%2FSensitive" target={target} icon={RiFileList3Line}>Collection</NavItem>
      <Location />
    </MemoryRouter>,
  );
  const link = screen.getByRole("link", { name: "Collection" });
  fireEvent.focus(link);
  await waitFor(() => expect(load).toHaveBeenCalledOnce());
  expect(screen.getByLabelText("location")).toHaveTextContent("/logs");
  fireEvent.click(link);
  expect(screen.getByLabelText("location")).toHaveTextContent("/collections/Case%2FSensitive/schema");
});

it("matches path boundaries and chooses the longest matching screen prefix", async () => {
  const collection = vi.spyOn(routeModules, "/collections");
  const specific = vi.fn(async () => ({}));
  Object.assign(routeModules, { "/collections/special": specific });
  try {
    await preloadRoute("/collections/special/schema?draft=1#fields");
    expect(specific).toHaveBeenCalledOnce();
    expect(collection).not.toHaveBeenCalled();
    await preloadRoute("/collections-other");
    await preloadRoute("/ext/operator-screen");
    await preloadRoute("https://example.com/collections");
    expect(specific).toHaveBeenCalledOnce();
    expect(collection).not.toHaveBeenCalled();
  } finally {
    Reflect.deleteProperty(routeModules, "/collections/special");
  }
});

it("handles a failed speculative import and retries on later intent", async () => {
  const load = vi.spyOn(routeModules, "/logs")
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValueOnce({ LogsPage: () => <div /> });
  await expect(preloadRoute("/logs")).resolves.toBeUndefined();
  await expect(preloadRoute("/logs")).resolves.toBeUndefined();
  expect(load).toHaveBeenCalledTimes(2);
});
