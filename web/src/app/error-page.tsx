export function ErrorPage() {
  return (
    <div className="space-y-2">
      <h1 className="text-2xl font-semibold tracking-tight">dextro can't reach its server</h1>
      <p className="text-muted-foreground">Check that the server is running, then reload the page.</p>
    </div>
  );
}
