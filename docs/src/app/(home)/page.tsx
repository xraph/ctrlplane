import Link from 'next/link';
import {
  Activity,
  ArrowRight,
  Box,
  ChevronDown,
  Cloud,
  Github,
  Globe,
  Lock,
  Package,
  Rocket,
  Shield,
  Zap,
} from 'lucide-react';
import { cn } from '@/lib/cn';
import { buttonVariants } from 'fumadocs-ui/components/ui/button';
import type { LucideIcon } from 'lucide-react';

const features: { icon: LucideIcon; title: string; description: string }[] = [
  {
    icon: Box,
    title: 'Instance Lifecycle',
    description:
      'Provision, start, stop, scale, and destroy tenant instances with a declarative state machine.',
  },
  {
    icon: Rocket,
    title: 'Zero-Downtime Deploys',
    description:
      'Rolling, blue-green, and canary strategies. Ship without dropping a single request.',
  },
  {
    icon: Shield,
    title: 'Multi-Tenant Isolation',
    description:
      'Every query scoped by tenant. Resource quotas, audit trails, and admin controls built in.',
  },
  {
    icon: Cloud,
    title: 'Provider Agnostic',
    description:
      'Kubernetes, Docker, AWS ECS, Fly.io, Nomad, or bring your own with a single interface.',
  },
  {
    icon: Activity,
    title: 'Health Monitoring',
    description:
      'HTTP, TCP, gRPC, and command checks with automatic recovery and status tracking.',
  },
  {
    icon: Globe,
    title: 'Networking & TLS',
    description:
      'Custom domains, automatic TLS certificates, route management, and traffic splitting.',
  },
  {
    icon: Lock,
    title: 'Secrets Management',
    description:
      'Pluggable vault interface for secure storage. Inject secrets as environment variables at deploy time.',
  },
  {
    icon: Zap,
    title: 'Event System',
    description:
      'Publish and subscribe event bus for lifecycle hooks. In-memory, NATS, or Redis backends.',
  },
];

const principles: { number: string; title: string; description: string }[] = [
  {
    number: '01',
    title: 'Library, not framework',
    description:
      'A collection of Go packages you import. No binaries to deploy, no runtime to manage, no opinions forced on your architecture.',
  },
  {
    number: '02',
    title: 'Provider agnostic',
    description:
      'Define infrastructure through a unified interface. Switch from Docker in development to Kubernetes in production without changing application code.',
  },
  {
    number: '03',
    title: 'Multi-tenant by default',
    description:
      'Tenant isolation is not bolted on. Every store query is scoped, every resource is quota-tracked, and every action is audit-logged from day one.',
  },
  {
    number: '04',
    title: 'Composable interfaces',
    description:
      'Every subsystem is defined by a Go interface. Use the built-in implementations or bring your own. Mix and match to fit your exact requirements.',
  },
];

const services = [
  'Instances',
  'Deploys',
  'Health',
  'Telemetry',
  'Network',
  'Secrets',
  'Admin',
  'Events',
];

const providers = [
  'Kubernetes',
  'Docker',
  'AWS',
  'Fly.io',
  'Nomad',
  'GCP',
  'Azure',
  'Custom',
];

const quickLinks = [
  { label: 'Architecture', href: '/docs/architecture' },
  { label: 'Concepts', href: '/docs/concepts/entities' },
  { label: 'Providers', href: '/docs/concepts/providers' },
  { label: 'Deploy Strategies', href: '/docs/guides/deploy-strategies' },
  { label: 'API Reference', href: '/docs/api-reference/http-api' },
];

const codeExample = `package main

import (
    "context"
    "log"
    "net/http"

    "github.com/xraph/ctrlplane/api"
    "github.com/xraph/ctrlplane/app"
    "github.com/xraph/ctrlplane/provider/docker"
    "github.com/xraph/ctrlplane/store/memory"
)

func main() {
    ctx := context.Background()
    store := memory.New()
    prov, _ := docker.New(docker.Config{})

    cp, err := app.New(
        app.WithStore(store),
        app.WithProvider("docker", prov),
        app.WithDefaultProvider("docker"),
    )
    if err != nil {
        log.Fatal(err)
    }

    cp.Start(ctx)
    log.Fatal(http.ListenAndServe(":8080",
        api.New(cp).Handler()))
}`;

export default function HomePage() {
  return (
    <main className="flex flex-1 flex-col">
      {/* ─── Hero ─── */}
      <section className="relative overflow-hidden">
        <div className="bg-grid absolute inset-0 [mask-image:linear-gradient(to_bottom,white_40%,transparent)]" />
        <div className="relative z-10 mx-auto max-w-4xl px-6 py-28 text-center sm:py-36">
          <div className="mb-8 inline-flex items-center gap-2 rounded-full border border-fd-border bg-fd-card px-4 py-1.5 text-sm text-fd-muted-foreground">
            <Package className="size-4" />
            Open Source Go Library
          </div>
          <h1 className="text-4xl font-bold tracking-tight text-fd-foreground sm:text-5xl lg:text-6xl">
            The control plane
            <br />
            your SaaS deserves
          </h1>
          <p className="mx-auto mt-6 max-w-2xl text-lg leading-relaxed text-fd-muted-foreground sm:text-xl">
            A composable Go library for deploying and managing multi-tenant
            application instances. Provider-agnostic. Interface-driven.
            Production-ready.
          </p>
          <div className="mt-10 flex flex-row items-center justify-center gap-3">
            <Link
              href="/docs/getting-started"
              className={cn(
                buttonVariants({ variant: 'primary' }),
                'gap-2 px-6 py-2.5',
              )}
            >
              Get Started
              <ArrowRight className="size-4" />
            </Link>
            <Link
              href="https://github.com/xraph/ctrlplane"
              className={cn(
                buttonVariants({ variant: 'outline' }),
                'gap-2 px-6 py-2.5',
              )}
            >
              <Github className="size-4" />
              GitHub
            </Link>
          </div>
        </div>
      </section>

      {/* ─── Code Example ─── */}
      <section className="px-6 py-24">
        <div className="mx-auto grid max-w-6xl grid-cols-1 items-center gap-12 lg:grid-cols-2 lg:gap-16">
          <div className="flex flex-col">
            <p className="text-sm font-medium uppercase tracking-wide text-fd-muted-foreground">
              Quick Start
            </p>
            <h2 className="mt-4 text-3xl font-bold tracking-tight text-fd-foreground sm:text-4xl">
              Up and running
              <br />
              in minutes
            </h2>
            <p className="mt-4 max-w-md leading-relaxed text-fd-muted-foreground">
              Import the packages you need, wire up a provider, and start
              serving. No YAML. No config files. No CLI. Just Go.
            </p>
          </div>
          <div className="overflow-hidden rounded-xl border border-fd-border bg-fd-card shadow-sm">
            <div className="flex items-center gap-2 border-b border-fd-border bg-fd-muted/50 px-4 py-3">
              <div className="flex gap-1.5">
                <span className="size-3 rounded-full bg-fd-border" />
                <span className="size-3 rounded-full bg-fd-border" />
                <span className="size-3 rounded-full bg-fd-border" />
              </div>
              <span className="ml-2 text-xs text-fd-muted-foreground">
                main.go
              </span>
            </div>
            <div className="overflow-x-auto p-5">
              <pre className="font-mono text-[13px] leading-relaxed text-fd-foreground/90">
                <code>{codeExample}</code>
              </pre>
            </div>
          </div>
        </div>
      </section>

      {/* ─── Features ─── */}
      <section className="px-6 py-24">
        <div className="mx-auto max-w-6xl">
          <div className="text-center">
            <h2 className="text-3xl font-bold tracking-tight text-fd-foreground sm:text-4xl">
              Everything you need
            </h2>
            <p className="mx-auto mt-4 max-w-2xl text-fd-muted-foreground">
              A complete toolkit for multi-tenant SaaS infrastructure, designed
              as composable Go packages.
            </p>
          </div>
          <div className="mt-16 grid grid-cols-1 gap-6 sm:grid-cols-2 lg:grid-cols-4">
            {features.map((feature) => (
              <div
                key={feature.title}
                className="group rounded-xl border border-fd-border p-6 transition-colors hover:bg-fd-card"
              >
                <div className="mb-4 flex size-10 items-center justify-center rounded-lg bg-fd-muted">
                  <feature.icon className="size-5 text-fd-muted-foreground transition-colors group-hover:text-fd-foreground" />
                </div>
                <h3 className="text-sm font-semibold text-fd-foreground">
                  {feature.title}
                </h3>
                <p className="mt-2 text-sm leading-relaxed text-fd-muted-foreground">
                  {feature.description}
                </p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ─── Architecture ─── */}
      <section className="px-6 py-24">
        <div className="mx-auto max-w-3xl text-center">
          <h2 className="text-3xl font-bold tracking-tight text-fd-foreground sm:text-4xl">
            Layered by design
          </h2>
          <p className="mx-auto mt-4 max-w-xl text-fd-muted-foreground">
            Every layer has a clean interface. Swap any component without
            touching the rest.
          </p>
          <div className="mt-16 flex flex-col items-center gap-3">
            {/* Application layer */}
            <div className="w-full rounded-xl border border-fd-primary/20 bg-fd-primary/[0.03] p-5">
              <p className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-fd-muted-foreground">
                Your Application
              </p>
              <p className="text-sm text-fd-muted-foreground">
                Standalone binary or Forge extension
              </p>
            </div>

            <div className="flex items-center justify-center gap-1 py-1">
              <ChevronDown className="size-4 text-fd-muted-foreground/60" />
              <ChevronDown className="size-4 text-fd-muted-foreground/60" />
              <ChevronDown className="size-4 text-fd-muted-foreground/60" />
            </div>

            {/* Services layer */}
            <div className="w-full rounded-xl border border-fd-border p-5">
              <p className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-fd-muted-foreground">
                Ctrl Plane Services
              </p>
              <div className="flex flex-wrap justify-center gap-2">
                {services.map((s) => (
                  <span
                    key={s}
                    className="inline-flex items-center rounded-md border border-fd-border bg-fd-muted/50 px-3 py-1 text-xs font-medium text-fd-foreground"
                  >
                    {s}
                  </span>
                ))}
              </div>
            </div>

            <div className="flex items-center justify-center gap-1 py-1">
              <ChevronDown className="size-4 text-fd-muted-foreground/60" />
              <ChevronDown className="size-4 text-fd-muted-foreground/60" />
              <ChevronDown className="size-4 text-fd-muted-foreground/60" />
            </div>

            {/* Provider layer */}
            <div className="w-full rounded-xl border border-fd-border p-5">
              <p className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-fd-muted-foreground">
                Provider Layer
              </p>
              <div className="flex flex-wrap justify-center gap-2">
                {providers.map((p) => (
                  <span
                    key={p}
                    className="inline-flex items-center rounded-md border border-fd-border bg-fd-muted/50 px-3 py-1 text-xs font-medium text-fd-foreground"
                  >
                    {p}
                  </span>
                ))}
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* ─── Principles ─── */}
      <section className="border-t border-fd-border px-6 py-24">
        <div className="mx-auto max-w-5xl">
          <h2 className="text-center text-3xl font-bold tracking-tight text-fd-foreground sm:text-4xl">
            Built on principles
          </h2>
          <div className="mt-16 grid grid-cols-1 gap-6 md:grid-cols-2">
            {principles.map((p) => (
              <div
                key={p.number}
                className="rounded-xl border border-fd-border p-8"
              >
                <p className="mb-4 font-mono text-sm text-fd-muted-foreground/60">
                  {p.number}
                </p>
                <h3 className="text-lg font-semibold text-fd-foreground">
                  {p.title}
                </h3>
                <p className="mt-3 text-sm leading-relaxed text-fd-muted-foreground">
                  {p.description}
                </p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ─── Final CTA ─── */}
      <section className="border-t border-fd-border px-6 py-24">
        <div className="mx-auto max-w-3xl text-center">
          <h2 className="text-3xl font-bold tracking-tight text-fd-foreground sm:text-4xl">
            Start building your control plane
          </h2>
          <p className="mt-4 text-fd-muted-foreground">
            Read the quick start guide or explore the full API reference.
          </p>
          <div className="mt-8 flex flex-row items-center justify-center gap-3">
            <Link
              href="/docs/getting-started"
              className={cn(
                buttonVariants({ variant: 'primary' }),
                'gap-2 px-6 py-2.5',
              )}
            >
              Quick Start
              <ArrowRight className="size-4" />
            </Link>
            <Link
              href="/docs"
              className={cn(
                buttonVariants({ variant: 'outline' }),
                'px-6 py-2.5',
              )}
            >
              Documentation
            </Link>
          </div>
          <div className="mt-16 flex flex-wrap items-center justify-center gap-x-1 gap-y-2">
            {quickLinks.map((link, i) => (
              <span key={link.href} className="flex items-center">
                {i > 0 && (
                  <span className="select-none px-2 text-fd-border">
                    &middot;
                  </span>
                )}
                <Link
                  href={link.href}
                  className="px-1 text-sm text-fd-muted-foreground transition-colors hover:text-fd-foreground"
                >
                  {link.label}
                </Link>
              </span>
            ))}
          </div>
        </div>
      </section>
    </main>
  );
}
