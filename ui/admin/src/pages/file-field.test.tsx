import { act, fireEvent, render, screen } from "@testing-library/react";
import { vi } from "vitest";
import { FileField } from "./record-edit-page";
import { callsTo, deferredStep } from "../test/backend-script";
import { memoryBackend } from "../test/memory-backend";
import { backendScript } from "../test/backend-script";
import { memoryHistory } from "../test/memory-history";
import { sessionWrapper } from "../test/harness";

const field = {
  name: "attachment",
  type: "file" as const,
  required: false,
  unique: false,
  indexed: false,
  ordinal: 1,
};
const item = {
  id: "file-1",
  field: "attachment",
  filename: "note.txt",
  contentType: "text/plain",
  size: 4,
  thumbnail: false,
};

function view(script = backendScript(), recordID = "record-1") {
  const backend = memoryBackend(script);
  return {
    script,
    ...render(
      <FileField collection="notes" recordID={recordID} field={field} />,
      { wrapper: sessionWrapper(backend, memoryHistory()) },
    ),
  };
}

describe("file field", () => {
  it("uploads, deletes, and revokes a downloaded object URL", async () => {
    const script = backendScript();
    script.listFiles.push({ value: [] });
    script.uploadFile.push({ value: item });
    script.createDownloadToken.push({ value: "token" });
    script.downloadBlob.push({ value: new Blob(["note"]) });
    script.deleteFile.push({ value: undefined });
    const create = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:test");
    const revoke = vi
      .spyOn(URL, "revokeObjectURL")
      .mockImplementation(() => {});
    const rendered = view(script);
    await screen.findByRole("button", { name: "Upload" });
    fireEvent.change(rendered.container.querySelector("input[type=file]")!, {
      target: {
        files: [new File(["note"], "note.txt", { type: "text/plain" })],
      },
    });
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));
    await screen.findByRole("listitem");
    fireEvent.click(screen.getByRole("button", { name: "Download" }));
    await act(async () => {});
    expect(create).toHaveBeenCalled();
    expect(revoke).toHaveBeenCalledWith("blob:test");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await act(async () => {});
    expect(callsTo(script, "deleteFile")).toHaveLength(1);
  });

  it("ignores a stale list completion after record changes", async () => {
    const stale = deferredStep<(typeof item)[]>();
    const script = backendScript();
    script.listFiles.push(stale.step, { value: [] });
    const backend = memoryBackend(script);
    const wrapper = sessionWrapper(backend, memoryHistory());
    const rendered = render(
      <FileField collection="notes" recordID="old" field={field} />,
      { wrapper },
    );
    rendered.rerender(
      <FileField collection="notes" recordID="new" field={field} />,
    );
    await act(async () => stale.settle({ value: [item] }));
    expect(screen.queryByText("note.txt")).not.toBeInTheDocument();
  });

  it("previews protected images only after minting a file-scoped token", async () => {
    const image = { ...item, contentType: "image/png", filename: "cover.png" };
    const script = backendScript();
    script.listFiles.push({ value: [image] });
    script.createDownloadToken.push({ value: "short-lived-token" });
    script.downloadBlob.push({
      value: new Blob(["png"], { type: "image/png" }),
    });
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:protected-preview");
    view(script);
    const button = await screen.findByRole("button", { name: "Preview" });
    fireEvent.click(button);
    await act(async () => {});
    expect(callsTo(script, "createDownloadToken")[0]?.detail).toEqual({
      id: "file-1",
      variant: "original",
    });
    expect(callsTo(script, "downloadBlob")[0]?.detail).toEqual({
      id: "file-1",
      token: "short-lived-token",
      variant: "original",
    });
    expect(
      await screen.findByRole("img", { name: "cover.png" }),
    ).toHaveAttribute("src", "blob:protected-preview");
  });

  it("uses only the server-provided same-origin public URL for an explicitly public file", async () => {
    const image = {
      ...item,
      contentType: "image/png",
      filename: "public.png",
      protected: false,
      publicUrl: "/api/files/11111111-1111-4111-8111-111111111111",
    };
    const script = backendScript();
    script.listFiles.push({ value: [image] });
    script.createDownloadToken.push({ fault: { kind: "unreachable" } });
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        new Response(new Blob(["png"], { type: "image/png" }), { status: 200 }),
      );
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:public-preview");
    view(script);
    expect(
      await screen.findByRole("link", { name: "Public link" }),
    ).toHaveAttribute("href", image.publicUrl);
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await act(async () => {});
    expect(fetchMock).toHaveBeenCalledWith(image.publicUrl, {
      credentials: "omit",
    });
    expect(callsTo(script, "createDownloadToken")).toHaveLength(0);
    expect(
      await screen.findByRole("img", { name: "public.png" }),
    ).toHaveAttribute("src", "blob:public-preview");
  });

  it("treats failed public downloads and previews as failures before creating object URLs", async () => {
    const image = {
      ...item,
      contentType: "image/png",
      filename: "public.png",
      protected: false,
      publicUrl: "/api/files/11111111-1111-4111-8111-111111111111",
    };
    const script = backendScript();
    script.listFiles.push({ value: [image] });
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response("missing", { status: 404 }))
      .mockResolvedValueOnce(new Response("failed", { status: 500 }));
    vi.stubGlobal("fetch", fetchMock);
    const create = vi.spyOn(URL, "createObjectURL");
    view(script);
    await screen.findByRole("button", { name: "Download" });
    fireEvent.click(screen.getByRole("button", { name: "Download" }));
    await screen.findByRole("alert");
    expect(screen.getByRole("alert")).toHaveTextContent("Download failed.");
    expect(create).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByText(
      "Preview failed. You may not have permission to read this protected file.",
    );
    expect(create).not.toHaveBeenCalled();
    expect(fetchMock).toHaveBeenNthCalledWith(1, image.publicUrl, {
      credentials: "omit",
    });
  });

  it("releases the active preview URL when the field unmounts", async () => {
    const image = { ...item, contentType: "image/png", filename: "cover.png" };
    const script = backendScript();
    script.listFiles.push({ value: [image] });
    script.createDownloadToken.push({ value: "short-lived-token" });
    script.downloadBlob.push({
      value: new Blob(["png"], { type: "image/png" }),
    });
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:preview");
    const revoke = vi
      .spyOn(URL, "revokeObjectURL")
      .mockImplementation(() => {});
    const rendered = view(script);
    fireEvent.click(await screen.findByRole("button", { name: "Preview" }));
    await screen.findByRole("img", { name: "cover.png" });
    rendered.unmount();
    expect(revoke).toHaveBeenCalledTimes(1);
    expect(revoke).toHaveBeenCalledWith("blob:preview");
  });
});
