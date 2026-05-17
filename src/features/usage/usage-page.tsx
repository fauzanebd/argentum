import { useState } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { OverviewTab } from "./overview-tab";
import { ThreadsTab } from "./threads-tab";
import { ChannelsTab } from "./channels-tab";
import { UsersTab } from "./users-tab";

function defaultFrom(): string {
  const d = new Date();
  d.setDate(d.getDate() - 30);
  return d.toISOString().slice(0, 10);
}

function defaultTo(): string {
  return new Date().toISOString().slice(0, 10);
}

export function UsagePage() {
  const [tab, setTab] = useState("overview");
  const [from, setFrom] = useState(defaultFrom());
  const [to, setTo] = useState(defaultTo());

  const showRange = tab !== "overview";

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-5xl mx-auto px-6 py-8">
        <h1 className="text-2xl font-bold mb-1">Usage</h1>
        <p className="text-sm text-muted-foreground mb-6">
          Cost and activity across threads, channels, and users.
        </p>

        <Tabs.Root value={tab} onValueChange={setTab}>
          <Tabs.List className="inline-flex border-b border-border mb-6">
            {[
              { id: "overview", label: "Overview" },
              { id: "threads", label: "Threads" },
              { id: "channels", label: "By channel" },
              { id: "users", label: "By user" },
            ].map((t) => (
              <Tabs.Trigger
                key={t.id}
                value={t.id}
                className={cn(
                  "px-4 py-2 text-sm border-b-2 transition-colors",
                  tab === t.id
                    ? "border-primary text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                {t.label}
              </Tabs.Trigger>
            ))}
          </Tabs.List>

          {showRange && (
            <div className="flex flex-wrap items-end gap-3 mb-6">
              <div className="space-y-1.5">
                <Label htmlFor="usage-from" className="text-xs">From</Label>
                <Input
                  id="usage-from"
                  type="date"
                  value={from}
                  onChange={(e) => setFrom(e.target.value)}
                  className="w-40"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="usage-to" className="text-xs">To</Label>
                <Input
                  id="usage-to"
                  type="date"
                  value={to}
                  onChange={(e) => setTo(e.target.value)}
                  className="w-40"
                />
              </div>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setFrom(defaultFrom());
                  setTo(defaultTo());
                }}
              >
                Last 30 days
              </Button>
            </div>
          )}

          <Tabs.Content value="overview">
            <OverviewTab />
          </Tabs.Content>
          <Tabs.Content value="threads">
            <ThreadsTab from={from} to={to} />
          </Tabs.Content>
          <Tabs.Content value="channels">
            <ChannelsTab from={from} to={to} />
          </Tabs.Content>
          <Tabs.Content value="users">
            <UsersTab from={from} to={to} />
          </Tabs.Content>
        </Tabs.Root>
      </div>
    </div>
  );
}
