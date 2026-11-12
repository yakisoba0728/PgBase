import { useQuery } from "@tanstack/react-query";
import { RiFileLine } from "@remixicon/react";
import type { StoredFile } from "../api/records";
import { fileUrlQuery } from "./queries";

/** A small square for one attachment: the server thumbnail for images, else a file icon. */
export function FileThumb({ file, enabled = true }: { file: StoredFile; enabled?: boolean }) {
  const image = file.thumbnail && file.contentType.startsWith("image/");
  const url = useQuery({ ...fileUrlQuery(file, "thumbnail"), enabled: image && enabled });
  return (
    <span className="file-thumb" title={file.filename}>
      {image && url.data ? <img src={url.data} alt="" loading="lazy" decoding="async" /> : <RiFileLine size={14} aria-hidden />}
    </span>
  );
}
