import { QueryClient, QueryClientProvider, useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { combineQueries, QueryView, useHeld, type ViewQuery } from "./QueryView";

afterEach(cleanup);

const base: ViewQuery<string[]> = { data: undefined, isPending: false, isError: false, error: null, isFetching: false, refetch: () => {} };
const view = (query: ViewQuery<string[]>) => (
  <QueryView query={query} empty={<p>nothing</p>}>{(data) => <p>rows {data.join(",")}</p>}</QueryView>
);

it("walks pending, failed, empty and loaded", () => {
  const { rerender } = render(view({ ...base, isPending: true }));
  expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true");
  rerender(view({ ...base, isError: true, error: new Error("boom") }));
  expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  expect(screen.queryByText(/rows/)).toBeNull();
  rerender(view({ ...base, data: [] }));
  expect(screen.getByText("nothing")).toBeInTheDocument();
  rerender(view({ ...base, data: ["a", "b"] }));
  expect(screen.getByText("rows a,b")).toBeInTheDocument();
  expect(screen.queryByRole("status")).toBeNull();
});

it("keeps loaded data when a refresh fails and offers a retry", () => {
  const refetch = vi.fn();
  render(view({ ...base, data: ["a"], isError: true, error: new Error("boom"), refetch }));
  expect(screen.getByText("rows a")).toBeInTheDocument();
  expect(screen.getByRole("status")).toHaveTextContent(/Couldn’t refresh/);
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  expect(refetch).toHaveBeenCalledOnce();
});

it("leaves a failed next page to LoadMore", () => {
  render(view({ ...base, data: ["a"], isError: true, error: new Error("boom"), isFetchNextPageError: true }));
  expect(screen.getByText("rows a")).toBeInTheDocument();
  expect(screen.queryByRole("status")).toBeNull();
});

it("treats a null result as loaded", () => {
  render(<QueryView query={{ ...base, data: null }}>{(data) => <p>{data === null ? "none" : "some"}</p>}</QueryView>);
  expect(screen.getByText("none")).toBeInTheDocument();
});

it("accepts useQuery and useInfiniteQuery results", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrap = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  function Both() {
    const one = useQuery({ queryKey: ["one"], queryFn: async () => ({ items: ["x"] }) });
    const paged = useInfiniteQuery({ queryKey: ["paged"], queryFn: async () => ["y"], initialPageParam: 0, getNextPageParam: () => undefined });
    return (
      <>
        <QueryView query={one} isEmpty={(data) => data.items.length === 0} empty={<p>empty</p>}>{(data) => <p>one {data.items[0]}</p>}</QueryView>
        <QueryView query={paged}>{(data) => <p>paged {data.pages.flat()[0]}</p>}</QueryView>
      </>
    );
  }
  render(<Both />, { wrapper: wrap });
  await act(async () => {});
  expect(await screen.findByText("one x")).toBeInTheDocument();
  expect(await screen.findByText("paged y")).toBeInTheDocument();
});

it("combines two queries: pending until both load, null counts as loaded, the empty one's error wins", () => {
  const a = vi.fn();
  const b = vi.fn();
  const loaded: ViewQuery<string[]> = { ...base, data: ["a"], refetch: a };
  const nothing: ViewQuery<null> = { data: null, isPending: false, isError: false, error: null, isFetching: false, refetch: b };
  expect(combineQueries(loaded, { ...nothing, data: undefined, isPending: true })).toMatchObject({ data: undefined, isPending: true });
  expect(combineQueries(loaded, nothing)).toMatchObject({ data: [["a"], null], isError: false });
  const refresh = new Error("refresh");
  const first = new Error("first");
  const failed = combineQueries({ ...loaded, isError: true, error: refresh }, { ...nothing, data: undefined, isError: true, error: first });
  expect(failed).toMatchObject({ data: undefined, isError: true, error: first });
  void failed.refetch();
  expect(a).toHaveBeenCalledOnce();
  expect(b).toHaveBeenCalledOnce();
  const stale = combineQueries({ ...loaded, isError: true, error: refresh }, nothing);
  expect(stale).toMatchObject({ data: [["a"], null], error: refresh });
  void stale.refetch();
  expect(b).toHaveBeenCalledOnce();
});

it("holds the shown rows, marked busy, while the same entity loads again; another entity starts blank", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const answers = new Map<string, (rows: string[]) => void>();
  function List({ entity, filter }: { entity: string; filter: string }) {
    const query = useHeld(useQuery({ queryKey: [entity, filter], queryFn: () => new Promise<string[]>((resolve) => answers.set(`${entity}:${filter}`, resolve)) }), entity);
    return <QueryView query={query}>{(rows) => <p>rows {rows.join(",")}</p>}</QueryView>;
  }
  const at = (entity: string, filter: string) => <QueryClientProvider client={client}><List entity={entity} filter={filter} /></QueryClientProvider>;
  const { rerender } = render(at("a", "1"));
  expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true");
  await waitFor(() => expect(answers.has("a:1")).toBe(true));
  act(() => answers.get("a:1")!(["x"]));
  expect(await screen.findByText("rows x")).toBeInTheDocument();
  expect(screen.queryByRole("progressbar")).toBeNull();
  // New parameters: the rows stay under the busy line, no skeleton.
  rerender(at("a", "2"));
  expect(screen.getByText("rows x")).toBeInTheDocument();
  expect(screen.getByRole("progressbar")).toHaveAttribute("aria-busy", "true");
  expect(screen.queryByRole("status")).toBeNull();
  await waitFor(() => expect(answers.has("a:2")).toBe(true));
  act(() => answers.get("a:2")!(["y"]));
  expect(await screen.findByText("rows y")).toBeInTheDocument();
  expect(screen.queryByRole("progressbar")).toBeNull();
  // A reset after a change holds them too.
  act(() => void client.resetQueries({ queryKey: ["a"] }));
  expect(await screen.findByRole("progressbar")).toBeInTheDocument();
  expect(screen.getByText("rows y")).toBeInTheDocument();
  expect(screen.queryByRole("status")).toBeNull();
  act(() => answers.get("a:2")!(["z"]));
  expect(await screen.findByText("rows z")).toBeInTheDocument();
  // Another entity never shows the previous one's rows.
  rerender(at("b", "2"));
  expect(screen.queryByText(/rows/)).toBeNull();
  expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true");
});

it("drops held rows when the load fails", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let fail!: (error: Error) => void;
  function List({ filter }: { filter: string }) {
    const query = useHeld(useQuery({ queryKey: ["held", filter], queryFn: () => (filter === "1" ? Promise.resolve(["x"]) : new Promise<string[]>((_, reject) => { fail = reject; })) }));
    return <QueryView query={query}>{(rows) => <p>rows {rows.join(",")}</p>}</QueryView>;
  }
  const { rerender } = render(<QueryClientProvider client={client}><List filter="1" /></QueryClientProvider>);
  expect(await screen.findByText("rows x")).toBeInTheDocument();
  rerender(<QueryClientProvider client={client}><List filter="2" /></QueryClientProvider>);
  expect(screen.getByText("rows x")).toBeInTheDocument();
  await waitFor(() => expect(fail).toBeDefined());
  act(() => fail(new Error("boom")));
  expect(await screen.findByRole("button", { name: "Retry" })).toBeInTheDocument();
  expect(screen.queryByText(/rows/)).toBeNull();
});
