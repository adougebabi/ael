const prefix = "ael-manual-review-v2:";
const projectPrefix = (project: string) => prefix + encodeURIComponent(project) + ":";
const key = (project: string, taskId: string, checkId: string) => projectPrefix(project) + encodeURIComponent(taskId) + ":" + encodeURIComponent(checkId);

export function readReviewDraft(project: string, taskId: string, checkId: string): any {
  if (!project) return null;
  try { return JSON.parse(sessionStorage.getItem(key(project, taskId, checkId)) || "null"); } catch { return null; }
}
export function writeReviewDraft(project: string, taskId: string, checkId: string, draft: unknown): boolean {
  if (!project) return false;
  try { sessionStorage.setItem(key(project, taskId, checkId), JSON.stringify(draft)); return true; } catch { return false; }
}
export function clearReviewDraft(project: string, taskId: string, checkId: string) {
  if (!project) return;
  try { sessionStorage.removeItem(key(project, taskId, checkId)); } catch { /* Storage is optional. */ }
}
export function pendingReviews(project: string): Array<{ taskId: string; checkId: string; title: string }> {
  const result: Array<{ taskId: string; checkId: string; title: string }> = [];
  if (!project) return result;
  try {
    for (let i = 0; i < sessionStorage.length; i++) {
      const storedKey = sessionStorage.key(i);
      if (!storedKey?.startsWith(projectPrefix(project))) continue;
      try {
        const draft = JSON.parse(sessionStorage.getItem(storedKey) || "null"), pending = draft?.pending;
        if (pending?.action === "review_result" && typeof pending.id === "string" && pending.unit === draft.checkId && pending.request_key)
          result.push({ taskId: pending.id, checkId: pending.unit, title: typeof draft.taskTitle === "string" ? draft.taskTitle : pending.id });
      } catch { /* Ignore an unrelated corrupt draft. */ }
    }
  } catch { /* Invalid or unavailable storage never grants a review. */ }
  return result;
}
export function pendingReviewForTask(project: string, taskId: string): string { return pendingReviews(project).find((item) => item.taskId === taskId)?.checkId || ""; }
