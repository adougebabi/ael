const prefix = "ael-knowledge-bookmarks-v1:";
const storageKey = (project: string) => prefix + encodeURIComponent(project);

export function readKnowledgeBookmarks(project: string): string[] {
  if (!project) return [];
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey(project)) || "[]");
    return Array.isArray(saved) ? saved.filter((id): id is string => typeof id === "string") : [];
  } catch { return []; }
}

export function toggleKnowledgeBookmark(project: string, id: string): string[] | null {
  if (!project || !id) return null;
  const previous = readKnowledgeBookmarks(project);
  const next = previous.includes(id) ? previous.filter((value) => value !== id) : [...previous, id];
  try {
    localStorage.setItem(storageKey(project), JSON.stringify(next));
    return next;
  } catch { return null; }
}
