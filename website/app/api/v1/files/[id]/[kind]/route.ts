import { NextResponse } from "next/server";

const PICTURES = new Set(["plate-05", "plate-07", "plate-10", "naryn", "amazon", "tour"]);

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
  if (id === "tour" && kind === "content") {
    return redirect("/demo/media/tour.mp4");
  }
  if (PICTURES.has(id) && (kind === "content" || kind === "thumbnail")) {
    return redirect(`/demo/media/${id}.webp`);
  }
  if (kind === "content") {
    return new NextResponse("This is a made-up file from the Tributary demo.\n", {
      headers: { "Content-Type": "text/plain", "Content-Disposition": 'attachment; filename="tributary-demo.txt"' },
    });
  }
  return new NextResponse(null, { status: 404 });
}
