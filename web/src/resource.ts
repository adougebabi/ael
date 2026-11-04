import { ref, onUnmounted, type Ref } from "vue";
import { restorePosition } from "./router";
export function useResource<T>(initial: T) {
  const data = ref(initial) as Ref<T>,
    loading = ref(false),
    error = ref(""),
    last = ref("");
  let seq = 0,
    disposed = false,
    first = true;
  onUnmounted(() => {
    disposed = true;
    seq++;
  });
  async function load(fetcher: () => Promise<T>, quiet = false) {
    const current = ++seq;
    if (!quiet) loading.value = true;
    try {
      const v = await fetcher();
      if (disposed || seq !== current) return;
      data.value = v;
      error.value = "";
      last.value = new Date().toLocaleTimeString("zh-CN");
      if (first) {
        first = false;
        await restorePosition();
      }
    } catch (e) {
      if (!disposed && seq === current) error.value = String(e);
    } finally {
      if (!disposed && seq === current) loading.value = false;
    }
  }
  return { data, loading, error, last, load };
}
