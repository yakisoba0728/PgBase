import { useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { RiFileLine } from "@remixicon/react";
import type { StoredFile } from "../api/records";
import { fileUrlQuery } from "./queries";

/** A small square for one attachment: the server thumbnail for images, else a file icon. */
export function FileThumb({ file }: { file: StoredFile }) {
  const image = file.thumbnail && file.contentType.startsWith("image/");
  const url = useQuery({ ...fileUrlQuery(file, "thumbnail"), enabled: image });
  // An expired token shows as a broken image: mint a fresh one, once.
  const retried = useRef(false);
  const onError = () => {
    if (retried.current) return;
    retried.current = true;
    void url.refetch();
  };
  return (
    <span className="file-thumb" title={file.filename}>
      {image && url.data ? <img src={url.data} alt="" loading="lazy" decoding="async" onError={onError} /> : <RiFileLine size={14} aria-hidden />}
    </span>
  );
}
