import Link from 'next/link';

export default function HomePage() {
  return (
    <main className="flex flex-col items-center justify-center flex-1 px-6 py-24 text-center">
      <p className="text-sm font-medium text-fd-muted-foreground mb-4 tracking-wide uppercase">
        Go library for SaaS infrastructure
      </p>
      <h1 className="text-4xl font-bold tracking-tight sm:text-5xl max-w-2xl">
        Deploy and manage tenant instances at scale
      </h1>
      <p className="mt-6 text-lg text-fd-muted-foreground max-w-xl leading-relaxed">
        Ctrl Plane is a composable Go library that gives you instance
        lifecycle management, zero-downtime deployments, health monitoring,
        and multi-tenant isolation &mdash; backed by any cloud provider.
      </p>
      <div className="flex flex-row gap-3 mt-8">
        <Link
          href="/docs"
          className="inline-flex items-center justify-center rounded-md bg-fd-primary px-5 py-2.5 text-sm font-medium text-fd-primary-foreground shadow-sm hover:bg-fd-primary/90 transition-colors"
        >
          Read the docs
        </Link>
        <Link
          href="/docs/getting-started"
          className="inline-flex items-center justify-center rounded-md border border-fd-border px-5 py-2.5 text-sm font-medium text-fd-foreground hover:bg-fd-accent transition-colors"
        >
          Quick start
        </Link>
      </div>
      <div className="mt-16 w-full max-w-3xl">
        <div className="rounded-lg border border-fd-border bg-fd-card p-6 text-left">
          <pre className="overflow-x-auto text-sm leading-relaxed">
            <code className="text-fd-muted-foreground">{`cp, err := app.New(
    app.WithStore(memoryStore),
    app.WithProvider("docker", dockerProvider),
    app.WithDefaultProvider("docker"),
)

// Start background workers and serve the API
cp.Start(ctx)
http.ListenAndServe(":8080", api.New(cp).Handler())`}</code>
          </pre>
        </div>
      </div>
    </main>
  );
}
