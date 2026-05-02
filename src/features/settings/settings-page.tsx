import { useState } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import { ConnectionsTab } from "./connections-tab";
import { PhonesTab } from "./phones-tab";
import { cn } from "@/lib/utils";

export function SettingsPage() {
  const [tab, setTab] = useState("connections");
  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-4xl mx-auto px-6 py-8">
        <h1 className="text-2xl font-bold mb-1">Settings</h1>
        <p className="text-sm text-muted-foreground mb-6">
          Manage your databases and authorised phone numbers.
        </p>
        <Tabs.Root value={tab} onValueChange={setTab}>
          <Tabs.List className="inline-flex border-b border-border mb-6">
            {[
              { id: "connections", label: "Databases" },
              { id: "phones", label: "Phone numbers" },
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
          <Tabs.Content value="connections">
            <ConnectionsTab />
          </Tabs.Content>
          <Tabs.Content value="phones">
            <PhonesTab />
          </Tabs.Content>
        </Tabs.Root>
      </div>
    </div>
  );
}
