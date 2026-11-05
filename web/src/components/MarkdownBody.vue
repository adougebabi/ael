<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import MarkdownIt from "markdown-it";
import { api, params } from "../api";
import { knowledgeHref, navigate, route } from "../router";
import { readableError } from "../display";
const props = defineProps<{ body: string; id: string }>();
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
function scroll(id: string) {
  document.getElementById(id)?.scrollIntoView({ block: "start" });
}
function scrollFragment(fragment: string) {
  const text = decodeURIComponent(fragment).toLowerCase();
  const heading = headings.value.find((h) => h.title.toLowerCase().replace(/\s+/g, "-") === text || h.id.toLowerCase() === text);
  if (heading) scroll(heading.id);
  return !!heading;
}
async function restoreFragment() {
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
  <div class="reader-layout">
    <nav v-if="headings.length > 1" class="toc" aria-label="正文目录">
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
      <article class="markdown" @click="click" v-html="html"></article>
    </div>
  </div>
</template>
