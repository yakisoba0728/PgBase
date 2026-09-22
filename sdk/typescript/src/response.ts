/** Read errors remain transport errors; only invalid JSON uses the supplied fallback. */
export async function responseJSON(response: Response, invalid: () => unknown): Promise<unknown> {
  const text = await response.text();
  if (response.status === 204) return undefined;
  try { return JSON.parse(text); }
  catch { return invalid(); }
}
