import { onUnmounted, ref, type Ref } from "vue";

// Each reader owns one explicitly requested object. Closing it or changing the
// surrounding route invalidates in-flight responses as well as its retry.
export function useObjectReader<T>() {
  const data = ref<T | null>(null) as Ref<T | null>,
    loading = ref(false),
    error = ref("");
  let sequence = 0,
    request: (() => Promise<T>) | undefined;
  function clear() {
    sequence++;
    request = undefined;
    data.value = null;
    loading.value = false;
    error.value = "";
  }
  async function read(fetcher: () => Promise<T>) {
    const current = ++sequence;
    request = fetcher;
    data.value = null;
    loading.value = true;
    error.value = "";
    try {
      const value = await fetcher();
      if (current === sequence) data.value = value;
    } catch (e) {
      if (current === sequence) error.value = String(e);
    } finally {
      if (current === sequence) loading.value = false;
    }
  }
  const retry = () => (request ? read(request) : Promise.resolve());
  onUnmounted(clear);
  return { data, loading, error, read, retry, clear };
}
