import { Suspense } from "react";
import { act, render, screen as view } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { preloadScreen, screen } from "./screens";

it("loads a screen ahead on its path, once, and then renders it without the fallback", async () => {
  const load = vi.fn(async () => ({ Ready: () => <p>ready</p> }));
  const Ready = screen("ready-test", load, "Ready");
  preloadScreen("/ready-test/sub");
  preloadScreen("/ready-test");
  preloadScreen("/other");
  await act(async () => {});
  expect(load).toHaveBeenCalledOnce();
  render(<Suspense fallback={<p>fallback</p>}><Ready /></Suspense>);
  expect(view.getByText("ready")).toBeInTheDocument();
  expect(view.queryByText("fallback")).toBeNull();
});
