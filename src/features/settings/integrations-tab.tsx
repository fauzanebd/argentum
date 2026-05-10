import { useState } from "react";
import { ArrowLeft } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { useToast } from "@/hooks/use-toast";

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

function TelegramIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      className={className}
      aria-hidden="true"
    >
      <path d="M9.999 15.2 9.84 19.4c.36 0 .516-.155.703-.34l1.687-1.61 3.495 2.555c.641.354 1.094.168 1.266-.594l2.297-10.762h.001c.203-.953-.343-1.325-.969-1.094L4.484 12.78c-.93.36-.918.875-.16 1.109l3.797 1.184 8.812-5.547c.414-.273.793-.121.484.152" />
    </svg>
  );
}

type Field = {
  name: string;
  label: string;
  placeholder: string;
  type?: "text" | "password";
};

type Integration = {
  id: string;
  name: string;
  description: string;
  helpText: string;
  icon: React.ComponentType<{ className?: string }>;
  fields: Field[];
};

const integrations: Integration[] = [
  {
    id: "slack",
    name: "Slack",
    description: "Send analytics digests and alerts to Slack channels.",
    helpText: "Posts go to the chosen channel via a bot installed in your workspace.",
    icon: SlackIcon,
    fields: [
      { name: "workspace", label: "Workspace URL", placeholder: "your-team.slack.com" },
      { name: "token", label: "Bot user OAuth token", placeholder: "xoxb-…", type: "password" },
      { name: "channel", label: "Default channel", placeholder: "#analytics" },
    ],
  },
  {
    id: "discord",
    name: "Discord",
    description: "Push agent updates to Discord servers and threads.",
    helpText: "The bot must be invited to the target server.",
    icon: DiscordIcon,
    fields: [
      { name: "server", label: "Server ID", placeholder: "123456789012345678" },
      { name: "token", label: "Bot token", placeholder: "MTAx…", type: "password" },
      { name: "channel", label: "Default channel ID", placeholder: "123456789012345678" },
    ],
  },
  {
    id: "lark",
    name: "Lark",
    description: "Deliver reports and alerts to Lark groups and chats.",
    helpText: "Create a custom app in Lark Developer Console and grant messaging scopes.",
    icon: LarkIcon,
    fields: [
      { name: "appId", label: "App ID", placeholder: "cli_a1b2c3d4e5f6g7h8" },
      { name: "appSecret", label: "App secret", placeholder: "•••••••••••••", type: "password" },
      { name: "chatId", label: "Default chat ID", placeholder: "oc_1a2b3c4d5e6f7g8h" },
    ],
  },
  {
    id: "telegram",
    name: "Telegram",
    description: "Receive answers and alerts in Telegram chats.",
    helpText: "Create a bot via @BotFather and add it to your chat.",
    icon: TelegramIcon,
    fields: [
      { name: "token", label: "Bot token", placeholder: "1234567890:AA…", type: "password" },
      { name: "chat", label: "Default chat ID", placeholder: "-1001234567890" },
    ],
  },
];

export function IntegrationsTab() {
  const [selected, setSelected] = useState<string | null>(null);

  const integration = integrations.find((i) => i.id === selected) ?? null;

  if (integration) {
    return (
      <IntegrationDetail
        integration={integration}
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
        <CardContent className="divide-y divide-border">
          {integrations.map(({ id, name, description, icon: Icon }) => (
            <div
              key={id}
              className="flex items-center justify-between gap-4 py-4 first:pt-0 last:pb-0"
            >
              <div className="flex items-center gap-3">
                <div className="flex h-10 w-10 items-center justify-center rounded-md border bg-muted">
                  <Icon className="h-5 w-5 text-foreground" />
                </div>
                <div className="flex items-center gap-2">
                  <div>
                    <p className="text-sm font-medium">{name}</p>
                    <p className="text-xs text-muted-foreground">{description}</p>
                  </div>
                </div>
              </div>
              <div className="flex items-center gap-2">
                <Badge variant="secondary">Coming soon</Badge>
                <Button variant="outline" onClick={() => setSelected(id)}>
                  Configure
                </Button>
              </div>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}

function IntegrationDetail({
  integration,
  onBack,
}: {
  integration: Integration;
  onBack: () => void;
}) {
  const { toast } = useToast();
  const Icon = integration.icon;

  function handleNotify() {
    toast({
      title: "Coming soon",
      description: `${integration.name} integration is in development. We'll let you know when it's ready.`,
    });
  }

  return (
    <div className="space-y-6">
      <Button variant="ghost" size="sm" onClick={onBack} className="-ml-2">
        <ArrowLeft className="h-4 w-4" />
        Back to integrations
      </Button>
      <Card>
        <CardHeader>
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-md border bg-muted">
              <Icon className="h-5 w-5 text-foreground" />
            </div>
            <div className="flex-1">
              <div className="flex items-center gap-2">
                <CardTitle>{integration.name}</CardTitle>
                <Badge variant="secondary">Coming soon</Badge>
              </div>
              <CardDescription className="mt-1">{integration.description}</CardDescription>
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="rounded-md border border-dashed bg-muted/40 p-3 text-xs text-muted-foreground">
            API integration is in development. Configuration is disabled until release.
          </div>
          {integration.fields.map((field) => (
            <div key={field.name} className="space-y-1.5">
              <Label>{field.label}</Label>
              <Input
                disabled
                type={field.type ?? "text"}
                placeholder={field.placeholder}
              />
            </div>
          ))}
          <p className="text-xs text-muted-foreground">{integration.helpText}</p>
        </CardContent>
        <CardFooter className="gap-2">
          <Button disabled>Save</Button>
          <Button variant="outline" onClick={handleNotify}>
            Notify me when ready
          </Button>
        </CardFooter>
      </Card>
    </div>
  );
}
