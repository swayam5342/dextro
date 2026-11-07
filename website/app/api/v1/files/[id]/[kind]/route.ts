import { NextResponse } from "next/server";

const PICTURES = new Set(["rennell-1786", "rennell-survey", "ganga-course", "tieffenthaler"]);

/** Relative, because behind a proxy the server doesn't know the address people used. */
function redirect(location: string) {
  return new NextResponse(null, { status: 307, headers: { Location: location } });
}

/**
 * The demo app loads pictures and downloads by address, which its stand-in API can't
 * answer because no script is involved. These are the demo's files, nothing else.
 */
export async function GET(_: Request, { params }: { params: Promise<{ id: string; kind: string }> }) {
  const { id, kind } = await params;
  if (id === "tour") {
    return redirect(kind === "content" ? "/media/demo.mp4" : "/media/files-dark.webp");
  }
  if (id === "rennell-copy") {
    return redirect("/media/demo/rennell-survey.webp");
  }
  if (PICTURES.has(id) && (kind === "content" || kind === "thumbnail")) {
    return redirect(`/media/demo/${id}.webp`);
  }
  if (kind === "content") {
    return new NextResponse("This is a made-up file from the Tributary demo.\n", {
      headers: { "Content-Type": "text/plain", "Content-Disposition": 'attachment; filename="tributary-demo.txt"' },
    });
  }
  return new NextResponse(null, { status: 404 });
}
