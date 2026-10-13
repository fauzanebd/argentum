import { useState } from "react";
import { ArrowLeft } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { DiscordTab } from "./discord-tab";
import { LarkTab } from "./lark-tab";
import { SlackTab } from "./slack-tab";

function WhatsAppIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden="true">
      <path
        fill="#25D366"
        d="M12 2.2a9.7 9.7 0 0 0-8.4 14.6L2.3 21.7l5-1.3A9.7 9.7 0 1 0 12 2.2Zm0 17.7c-1.5 0-2.9-.4-4.1-1.1l-.3-.2-3 .8.8-2.9-.2-.3A8 8 0 1 1 12 19.9Zm4.4-6c-.2-.1-1.4-.7-1.6-.8-.2-.1-.4-.1-.5.1l-.8.9c-.1.2-.3.2-.5.1a6.5 6.5 0 0 1-3.2-2.8c-.2-.4.2-.4.7-1.3.1-.2 0-.3 0-.4l-.7-1.8c-.2-.5-.4-.4-.5-.4h-.5a.9.9 0 0 0-.7.3 2.8 2.8 0 0 0-.9 2.1 4.9 4.9 0 0 0 1 2.6 11.2 11.2 0 0 0 4.3 3.8c1.6.7 2.2.7 3 .6.5-.1 1.4-.6 1.6-1.1.2-.6.2-1 .1-1.1l-.5-.3Z"
      />
    </svg>
  );
}

function SlackIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden="true">
      <path fill="#E01E5A" d="M5 14.5a2 2 0 1 1 0-4h2v4H5Zm1-4a2 2 0 0 1 0-4 2 2 0 0 1 2 2v2H6Z" />
      <path fill="#36C5F0" d="M9.5 5a2 2 0 1 1 4 0v2h-4V5Zm0 1a2 2 0 0 1 4 0 2 2 0 0 1-2 2H9.5V6Z" />
      <path fill="#2EB67D" d="M19 9.5a2 2 0 1 1 0 4h-2v-4h2Zm-1 4a2 2 0 0 1 0 4 2 2 0 0 1-2-2v-2h2Z" />
      <path fill="#ECB22E" d="M14.5 19a2 2 0 1 1-4 0v-2h4v2Zm0-1a2 2 0 0 1-4 0 2 2 0 0 1 2-2h2v2Z" />
    </svg>
  );
}

function DiscordIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      className={className}
      aria-hidden="true"
    >
      <path d="M20.317 4.369A19.79 19.79 0 0 0 16.558 3.2a.075.075 0 0 0-.079.037c-.34.607-.719 1.398-.984 2.02a18.27 18.27 0 0 0-5.487 0 12.51 12.51 0 0 0-.997-2.02.077.077 0 0 0-.079-.037A19.736 19.736 0 0 0 5.176 4.37a.07.07 0 0 0-.032.027C2.272 8.685 1.49 12.872 1.873 17.011a.082.082 0 0 0 .031.056 19.91 19.91 0 0 0 5.993 3.030.078.078 0 0 0 .084-.028c.462-.63.873-1.295 1.226-1.994a.076.076 0 0 0-.041-.105 13.13 13.13 0 0 1-1.872-.892.077.077 0 0 1-.008-.128c.126-.094.252-.192.371-.291a.074.074 0 0 1 .077-.01c3.928 1.793 8.18 1.793 12.061 0a.074.074 0 0 1 .078.009c.12.099.245.198.372.292a.077.077 0 0 1-.006.127 12.31 12.31 0 0 1-1.873.892.077.077 0 0 0-.041.106c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.84 19.84 0 0 0 6.002-3.03.077.077 0 0 0 .032-.054c.5-4.788-.838-8.94-3.548-12.615a.061.061 0 0 0-.031-.028zM8.02 14.49c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.956-2.419 2.157-2.419 1.21 0 2.176 1.094 2.157 2.42 0 1.333-.956 2.418-2.157 2.418zm7.974 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.094 2.157 2.42 0 1.333-.946 2.418-2.157 2.418z" />
    </svg>
  );
}

function LarkIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden="true">
      <path
        fill="#00D6B9"
        d="M3.2 14.6c0-3.3 2.4-6.2 5.6-6.9 1.5-.3 3 0 4.3.7l-7 7c-1.5 1.5-2.9.3-2.9-.8z"
      />
      <path
        fill="#3370FF"
        d="M20.6 6.4c.4 0 .6.4.4.7-2.1 3.6-5.4 6.4-9.4 7.8-1.3.5-2.7.8-4.1.9-.6 0-1-.6-.6-1L17.3 4.5c.5-.5 1.2-.8 1.9-.8h1.4z"
      />
      <path
        fill="#133C9A"
        d="M14.4 14.7c2.2-.9 4.1-2.3 5.7-4.1.4-.4 1.1-.2 1.1.4v3.8c0 1.6-.9 3.1-2.3 3.9l-3.2 1.7c-1.4.8-3.1.4-4.1-.8l-1.4-1.7c1.5-.8 2.9-1.9 4.2-3.2z"
      />
    </svg>
  );
}

/**
 * What a row's button does. Required, and only these two kinds, so that a row
 * cannot be added to this list without something real behind its button.
 *
 * That is the rule this tab broke until RIZ-100 (2026-10-13): Telegram had a
 * row with a not-yet-released badge and a Configure button, which opened a
 * form whose every input and Save were disabled, beside a button offering to
 * tell you when it shipped that showed a toast and recorded nothing. No
 * Telegram backend existed. The landing page had already dropped the same claim in T-18, for the
 * same reason: a listed integration is a promise somebody repeats. A channel
 * that is not built goes in `docs/plan/backlog.md`, not here.
 *
 * - `configure` opens the channel's own config panel in place of the list.
 * - `open-tab` hands off to another Settings tab that already does the work.
 *   WhatsApp is the case: its allowlist lives on Phone numbers, and a second
 *   copy of that screen here would be two places to edit one list.
 */
type IntegrationAction =
  | { kind: "configure"; panel: React.ComponentType }
  | { kind: "open-tab"; tab: "phones"; label: "Manage numbers" };

type Integration = {
  id: string;
  name: string;
  description: string;
  icon: React.ComponentType<{ className?: string }>;
  action: IntegrationAction;
};

const integrations: Integration[] = [
  {
    id: "whatsapp",
    name: "WhatsApp",
    // "from numbers you authorise", and not "connect your WhatsApp": the
    // operator configures the WhatsApp number itself, and what a tenant
    // controls is which senders it answers.
    description: "Chat with the Argentum bot from WhatsApp, from numbers you authorise.",
    icon: WhatsAppIcon,
    action: { kind: "open-tab", tab: "phones", label: "Manage numbers" },
  },
  {
    id: "slack",
    name: "Slack",
    description: "Chat with the Argentum bot from Slack @mentions or DMs.",
    icon: SlackIcon,
    action: { kind: "configure", panel: SlackTab },
  },
  {
    id: "discord",
    name: "Discord",
    description: "Chat with the Argentum bot from Discord DMs or @mentions.",
    icon: DiscordIcon,
    action: { kind: "configure", panel: DiscordTab },
  },
  {
    id: "lark",
    name: "Lark",
    description: "Chat with the Argentum bot via Lark @mentions.",
    icon: LarkIcon,
    action: { kind: "configure", panel: LarkTab },
  },
];

/**
 * The Settings tab is local state in `SettingsPage` rather than a URL, so a
 * row that hands off to another tab does it through a callback from there.
 * Only Phone numbers is a target today, hence one named prop instead of a
 * generic `onOpenTab(id)` that would accept tabs nothing links to.
 */
export function IntegrationsTab({ onOpenPhones }: { onOpenPhones: () => void }) {
  const [selected, setSelected] = useState<string | null>(null);

  const integration = integrations.find((i) => i.id === selected) ?? null;

  if (integration && integration.action.kind === "configure") {
    return (
      <IntegrationDetail
        integration={integration}
        panel={integration.action.panel}
        onBack={() => setSelected(null)}
      />
    );
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Integrations</CardTitle>
          <CardDescription>
            Connect Argentum to your team's messaging tools. More integrations on the way.
          </CardDescription>
        </CardHeader>
        <CardContent role="list" className="divide-y divide-border">
          {integrations.map(({ id, name, description, icon: Icon, action }) => (
            <div
              key={id}
              role="listitem"
              aria-label={name}
              className="flex items-center justify-between gap-4 py-4 first:pt-0 last:pb-0"
            >
              <div className="flex items-center gap-3">
                <div className="flex h-10 w-10 items-center justify-center rounded-md border bg-muted">
                  <Icon className="h-5 w-5 text-foreground" />
                </div>
                <div>
                  <p className="text-sm font-medium">{name}</p>
                  <p className="text-xs text-muted-foreground">{description}</p>
                </div>
              </div>
              {action.kind === "configure" ? (
                <Button variant="outline" onClick={() => setSelected(id)}>
                  Configure
                </Button>
              ) : (
                <Button variant="outline" onClick={onOpenPhones}>
                  {action.label}
                </Button>
              )}
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}

function IntegrationDetail({
  integration,
  panel: Panel,
  onBack,
}: {
  integration: Integration;
  panel: React.ComponentType;
  onBack: () => void;
}) {
  const Icon = integration.icon;

  return (
    <div className="space-y-6">
      <Button variant="ghost" size="sm" onClick={onBack} className="-ml-2">
        <ArrowLeft className="h-4 w-4" />
        Back to integrations
      </Button>
      <div className="flex items-center gap-3">
        <div className="flex h-10 w-10 items-center justify-center rounded-md border bg-muted">
          <Icon className="h-5 w-5 text-foreground" />
        </div>
        <div>
          <h2 className="text-lg font-semibold">{integration.name}</h2>
          <p className="text-sm text-muted-foreground">{integration.description}</p>
        </div>
      </div>
      <Panel />
    </div>
  );
}
