import "../styles/widgets.css";

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
}

/** A read-only result table; cells are monospace and never parsed, the parent bounds its scroll area. */
export function DataGrid({ label, columns, rows }: DataGridProps) {
  return (
    <table className="grid data-grid" aria-label={label}>
      <thead>
        <tr>
          {columns.map((column, position) => (
            <th key={position}>
              {column.name} {column.type && <span className="th-type">{column.type}</span>}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row, position) => (
          <tr key={position}>
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
      </tbody>
    </table>
  );
}
