# Tributary website

The public site: the home page, the API guide, the self-hosting guide and a demo. It's a Next.js app, separate from the [privacy and terms pages](https://swayamcyber.in/dextro-site/) that Google's consent screen links to.

## Deploying to Vercel

1. Import the GitHub repo.
2. Set **Root Directory** to `website`.
3. Deploy. Vercel detects Next.js, and no environment variables are needed.

## Working on it

```sh
pnpm install
pnpm dev          # http://localhost:3200
pnpm build
pnpm typecheck
```

## The demo

`/demo` is the real React app from `web/`, not a copy of its look. `demo-api/demo-api.js` replaces `fetch` and `XMLHttpRequest` in the browser and answers from made-up data, so nothing is sent anywhere and a reload starts again.

The built app lives in `public/demo` and is committed, because Vercel only builds this folder. Rebuild it when the app changes:

```sh
pnpm build:demo
```

New API routes the app calls need adding to `demo-api.js`. Pictures and downloads load by address, which no script can answer, so `app/api/v1/files/[id]/[kind]/route.ts` serves those.

## The API examples

The samples in the API guide are the files in `_examples`, which the page reads when it builds. Change the file and the page follows.

## Pictures

The maps are Harold N. Fisk's 1944 survey for the US Army Corps of Engineers, and the demo's satellite pictures are from NASA. All are public domain. `demo-api/credits.json` lists each one with its source.
