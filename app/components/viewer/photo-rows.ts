// Packs photos, given as width-to-height ratios, into rows whose combined
// ratio is near the target. A photo joins a row when it brings the row nearer
// the target, even past it, so a wide photo after two ordinary ones shortens
// the row rather than leaving it two-thirds empty. Each row's share is the
// fraction of the width it fills. Rows stretch to the full width, except the
// last and any row well short of the target, such as a lone portrait before a
// panorama; those keep their natural size.
export function photoRows(ratios: number[], target: number) {
  const rows: { items: number[]; sum: number; share: number }[] = [];
  ratios.forEach((ratio, index) => {
    const last = rows.at(-1);
    if (!last || last.sum + ratio - target > target - last.sum)
      rows.push({ items: [index], sum: ratio, share: 1 });
    else {
      last.items.push(index);
      last.sum += ratio;
    }
  });
  for (const [index, row] of rows.entries())
    if (index === rows.length - 1 || row.sum < target * 0.7)
      row.share = Math.min(1, row.sum / target);
  return rows;
}
