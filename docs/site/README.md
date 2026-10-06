# Customer docs

The pages in this folder are the "Collector" section of the docs at
https://otherlode.dev/docs. After a `v*.*.*` tag's release, the `docs`
job in `.github/workflows/release.yml` copies this folder into
`otherlode.dev` at `src/content/docs/collector/<tag>/` and opens a pull
request there. Each release keeps its own copy, so the site serves every
release's docs and a changes page that diffs each release against the
one before it. otherlode.dev ADRs 0001 and 0002 have the reasons.

A change that alters what a customer sees or sets should change its page
here, in the same pull request. Operator and contributor material stays
in the README. Keep a page's file name across releases. A renamed file
reads as one page removed and another added, and the release menu cannot
link the two.

This folder is what the next release says. Each release's copy in
otherlode.dev, at `src/content/docs/collector/<tag>/`, is edited there
when it is wrong about that release. If the mistake is still in this
folder, fix it here too, or the next release copies it again.

A page is a Markdown file with this frontmatter:

```md
---
title: Page title
description: One sentence for search results and link previews.
order: 10
---
```

`title` becomes the page's `h1`, so the Markdown starts at `##`. `order`
sorts the pages in this section, lowest first. The file name gives the
URL: `attach.md` is `/docs/collector/attach`. A file whose name starts
with `_` is not a page, and this README is not copied.

The site's CSP allows no inline script or style, so a page must not use
raw HTML with `<script>`, `<style>` or `style=`. The site's build fails
on them. To preview a page, copy it into a checkout of `otherlode.dev`
and run `npm run dev` there.
