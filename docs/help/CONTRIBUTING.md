---
id: contributing
kind: reference
title: Adding a help page
summary: How a feature adds its own guide or reference article, and a deep link that opens the right screen.
icon: book
order: 900
related: []
references: []
---

Add a Markdown file under `docs/help/guides/` or `docs/help/reference/`. The file is embedded in the binary and served at `GET /api/v1/help`. Rebuild with `make build` so the running app picks it up.

Describe only what the code does today. If the screen is not in the app yet, set `comingSoon: true` and say so in the first paragraph. Do not invent buttons, scopes, or steps.

## Frontmatter

```yaml
---
id: my-feature
kind: reference
title: My feature
summary: One sentence, shown on the tile and in search.
icon: box
order: 260
popular: false
comingSoon: false
page: docker
related:
  - docker
references:
  - snapshots
---
```

`kind` is `guide` for a task (it becomes a tile on the Help landing page) or `reference` for a feature article. `icon` is a name from the map in `web/src/components/help/icons.ts`. `page` is optional. Set it only when this article is the one the `?` on that nav page should open, and only on one article per page.

`related` and `references` are other article ids. Guides use `references` for the "In the reference" list.

The title in the app comes from `title`. Start the body with a paragraph or a `##` heading, not a duplicate `#` title.

## Deep links

Buttons use ids from `docs/help/routes.json`, not a path typed in the article:

```markdown
[[route:settings.remote|Open Remote access]]
```

Add a new destination by adding one object to that file, then give the target element a matching `id` (or a `view` the page already understands). An id that is not in the map fails `go test`. A route with `"available": false` must point `help` at an article. The button still renders, carries a Coming soon badge, and opens that article instead of a missing page.

Link to another article with `[[help:docker|Docker]]`.

## Callouts and code

```markdown
> [!TIP]
> A short hint.

> [!WARNING]
> Something that can lock you out or delete data.

```

Fence code with a language tag. The page adds a copy button. Headings become anchors on their own.
