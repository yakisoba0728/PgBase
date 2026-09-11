"""Reject workstation and ephemeral absolute paths in checked-in review evidence."""
from pathlib import Path
import re, sys
root = Path(__file__).resolve().parents[1]
files = [root / "docs/workstreams/n6-n9" / name for name in ("N7-review.md", "N8-browser.md", "N8-review.md", "integration.md")]
bad = []
for path in files:
    text = path.read_text(encoding="utf-8")
    for line_no, line in enumerate(text.splitlines(), 1):
        if re.search(r"(?:/Users/|/tmp/|file://)", line):
            bad.append(f"{path.relative_to(root)}:{line_no}")
if bad:
    print("non-portable evidence paths:\n" + "\n".join(bad)); sys.exit(1)
print(f"portable review evidence: {len(files)} documents checked; absolute workstation paths: 0")
