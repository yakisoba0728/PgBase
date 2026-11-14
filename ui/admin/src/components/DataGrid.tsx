export interface GridCell {
  /** Exact text; null renders as NULL. */
  text: string | null;
  numeric?: boolean;
  /** JSON, bytes and similar raw values are muted. */
  muted?: boolean;
}

interface DataGridProps {
  label: string;
  columns: { name: string; type?: string }[];
  rows: GridCell[][];
  /** Only a slice is rendered: `rows` start at row `start` of `total`, with spacer heights (px) for the rows left out. */
  virtual?: { start: number; total: number; before: number; after: number };
  /** CSS min-width per column, so widths hold steady while the slice changes. */
  widths?: string[];
}

function Spacer({ height, span }: { height: number; span: number }) {
  return height > 0 ? <tr aria-hidden><td colSpan={span} style={{ height, padding: 0, border: 0 }} /></tr> : null;
}

/** A read-only result table; cells are monospace and never parsed, the parent bounds its scroll area. */
export function DataGrid({ label, columns, rows, virtual, widths }: DataGridProps) {
  return (
    <table className="grid data-grid" aria-label={label} aria-rowcount={virtual && virtual.total + 1}>
      <thead>
        <tr aria-rowindex={virtual && 1}>
          {columns.map((column, position) => (
            <th key={position} style={widths?.[position] ? { minWidth: widths[position] } : undefined}>
              {column.name} {column.type && <span className="th-type">{column.type}</span>}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {virtual && <Spacer height={virtual.before} span={columns.length} />}
        {rows.map((row, position) => (
          <tr key={position} aria-rowindex={virtual && virtual.start + position + 2}>
            {row.map((cell, column) =>
              cell.text === null ? (
                <td key={column} className="data-grid-null">NULL</td>
              ) : (
                <td key={column} className={cell.numeric ? "num" : undefined} data-muted={cell.muted ? "" : undefined} title={cell.text.length > 40 ? cell.text : undefined}>
                  {cell.text}
                </td>
              ),
            )}
          </tr>
        ))}
        {virtual && <Spacer height={virtual.after} span={columns.length} />}
      </tbody>
    </table>
  );
}

