import { act, fireEvent, render, screen } from "@testing-library/react";
import { vi } from "vitest";
import { FileField } from "./record-edit-page";
import { callsTo, deferredStep } from "../test/backend-script";
import { memoryBackend } from "../test/memory-backend";
import { backendScript } from "../test/backend-script";
import { memoryHistory } from "../test/memory-history";
import { sessionWrapper } from "../test/harness";

const field = { name: "attachment", type: "file" as const, required: false, unique: false, indexed: false, ordinal: 1 };
const item = { id: "file-1", field: "attachment", filename: "note.txt", contentType: "text/plain", size: 4, thumbnail: false };

function view(script = backendScript(), recordID = "record-1") {
  const backend = memoryBackend(script);
  return { script, ...render(<FileField collection="notes" recordID={recordID} field={field} />, { wrapper: sessionWrapper(backend, memoryHistory()) }) };
}

describe("file field", () => {
  it("uploads, deletes, and revokes a downloaded object URL", async () => {
    const script = backendScript();
    script.listFiles.push({ value: [] }); script.uploadFile.push({ value: item }); script.createDownloadToken.push({ value: "token" }); script.downloadBlob.push({ value: new Blob(["note"]) }); script.deleteFile.push({ value: undefined });
    const create = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:test"); const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    const rendered = view(script); await screen.findByRole("button", { name: "Upload" });
    fireEvent.change(rendered.container.querySelector("input[type=file]")!, { target: { files: [new File(["note"], "note.txt", { type: "text/plain" })] } });
    fireEvent.click(screen.getByRole("button", { name: "Upload" })); await screen.findByRole("listitem");
    fireEvent.click(screen.getByRole("button", { name: "Download" })); await act(async () => {});
    expect(create).toHaveBeenCalled(); expect(revoke).toHaveBeenCalledWith("blob:test");
    fireEvent.click(screen.getByRole("button", { name: "Delete" })); await act(async () => {});
    expect(callsTo(script, "deleteFile")).toHaveLength(1);
  });

  it("ignores a stale list completion after record changes", async () => {
    const stale = deferredStep<typeof item[]>(); const script = backendScript(); script.listFiles.push(stale.step, { value: [] });
    const backend = memoryBackend(script); const wrapper = sessionWrapper(backend, memoryHistory());
    const rendered = render(<FileField collection="notes" recordID="old" field={field} />, { wrapper });
    rendered.rerender(<FileField collection="notes" recordID="new" field={field} />);
    await act(async () => stale.settle({ value: [item] }));
    expect(screen.queryByText("note.txt")).not.toBeInTheDocument();
  });
});
