import { RiCloseLine, RiDownload2Line, RiFileLine } from "@remixicon/react";
import { useT } from "../i18n";
import { IconButton } from "./Button";
import { useModal } from "./Dialog";
import { Loader } from "./Feedback";
import { formatBytes } from "../app/format";

interface PreviewFile {
  name: string;
  size: number;
  contentType: string;
}

interface FilePreviewProps {
  /** null closes the dialog. */
  file: PreviewFile | null;
  /** Same-origin URL of the original; undefined while it is being prepared. */
  url: string | undefined;
  error?: string;
  onClose: () => void;
}

/** A modal look at one file: images are shown, anything else gets its name, size and a download link. */
export function FilePreview({ file, url, error, onClose }: FilePreviewProps) {
  const t = useT();
  const modal = useModal(file !== null, onClose);
  const image = file?.contentType.startsWith("image/");
  return (
    <dialog className="file-preview" aria-label={file?.name} {...modal}>
      {file && (
        <>
          <header className="file-preview-head">
            <span className="file-preview-name" title={file.name}>{file.name}</span>
            <span className="txt-hint">{formatBytes(file.size)} · {file.contentType || "?"}</span>
            {url && (
              <a className="icon-btn" href={url} download={file.name} aria-label={t("file.download")} title={t("file.download")}>
                <RiDownload2Line size={16} aria-hidden />
              </a>
            )}
            <IconButton label={t("common.close")} icon={RiCloseLine} onClick={onClose} autoFocus />
          </header>
          <div className="file-preview-body">
            {error ? (
              <p className="txt-danger">{error}</p>
            ) : !url ? (
              <Loader />
            ) : image ? (
              <img src={url} alt={file.name} />
            ) : (
              <div className="file-preview-other">
                <RiFileLine size={32} aria-hidden />
                <p>{t("file.noPreview")}</p>
                <a className="btn btn-secondary btn-sm" href={url} download={file.name}>
                  <RiDownload2Line size={16} aria-hidden />
                  {t("file.download")}
                </a>
              </div>
            )}
          </div>
        </>
      )}
    </dialog>
  );
}
