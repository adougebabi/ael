<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import MarkdownIt from "markdown-it";
import { api, params } from "../api";
import { knowledgeHref, navigate, route } from "../router";
import { readableError } from "../display";
const props = withDefaults(defineProps<{ body: string; id: string; tocRight?: boolean; showBody?: boolean }>(), { showBody: true });
const emit = defineEmits<{ showBody: [] }>();
const linkError = ref("");
const md = new MarkdownIt({ html: false, linkify: false, typographer: false });
md.disable("image");
const valid = md.validateLink.bind(md);
md.validateLink = (url) => valid(url) && !/^data:|^\/\//i.test(url);
const headings = computed(() =>
  md
    .parse(props.body, {})
    .flatMap((t, i, all) =>
      t.type === "heading_open"
        ? [
            {
              id: "doc-" + encodeURIComponent(props.id) + "-" + i,
              title: all[i + 1]?.content || "",
              level: t.tag,
            },
          ]
        : [],
    ),
);
md.renderer.rules.heading_open = (tokens, i, options, _env, self) => {
  tokens[i].attrSet("id", "doc-" + encodeURIComponent(props.id) + "-" + i);
  return self.renderToken(tokens, i, options);
};
md.renderer.rules.link_open = (tokens, i, options, _env, self) => {
  tokens[i].attrSet("rel", "noopener noreferrer");
  return self.renderToken(tokens, i, options);
};
const html = computed(() => md.render(props.body));
async function scroll(id: string) {
  if (!props.showBody) { emit("showBody"); await nextTick(); }
  document.getElementById(id)?.scrollIntoView({ block: "start" });
}
function scrollFragment(fragment: string) {
  const text = decodeURIComponent(fragment).toLowerCase();
  const heading = headings.value.find((h) => h.title.toLowerCase().replace(/\s+/g, "-") === text || h.id.toLowerCase() === text);
  if (heading) scroll(heading.id);
  return !!heading;
}
async function restoreFragment() {
  if (!props.showBody) return;
  if (route.value.path !== "/knowledge/" + encodeURIComponent(props.id)) return;
  const fragment = route.value.query.get("fragment");
  if (fragment) { await nextTick(); scrollFragment(fragment); }
}
onMounted(restoreFragment);
watch(() => [props.body, route.value.key], restoreFragment);
async function click(e: MouseEvent) {
  const a = (e.target as HTMLElement).closest("a");
  if (!a) return;
  const target = a.getAttribute("href") || "";
  if (/^https?:|^mailto:/i.test(target)) return;
  e.preventDefault();
  linkError.value = "";
  if (target.startsWith("#")) {
    if (scrollFragment(target.slice(1))) return;
  }
  try {
    const v = await api(
      "/api/knowledge-link?" + params({ id: props.id, href: target }),
    );
    navigate(knowledgeHref(v.id, "", v.fragment || "").slice(1));
  } catch (e) {
    linkError.value = String(e);
  }
}
</script>
<template>
  <div class="reader-layout" :class="{ 'reader-layout-right': tocRight }">
    <nav v-if="!tocRight && headings.length > 1" class="toc" aria-label="正文目录">
      <strong>本文目录</strong
      ><button v-for="h in headings" :key="h.id" @click="scroll(h.id)">
        {{ h.title }}
      </button>
    </nav>
    <div class="reader">
      <div v-if="linkError" role="alert" class="alert">
        {{ readableError(linkError) }}
        <details>
          <summary>链接原因</summary>
          <pre>{{ linkError }}</pre>
        </details>
      </div>
      <slot name="before" />
      <article v-if="showBody" class="markdown" @click="click" v-html="html"></article>
      <slot name="after" />
    </div>
    <aside v-if="tocRight" class="reader-sidebar">
      <nav v-if="headings.length" class="toc" aria-label="正文目录"><strong>本页目录</strong><button v-for="h in headings" :key="h.id" :class="h.level" @click="scroll(h.id)">{{ h.title }}</button></nav>
      <slot name="sidebar" />
    </aside>
  </div>
</template>
