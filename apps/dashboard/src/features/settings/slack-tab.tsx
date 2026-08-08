import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api } from "@/lib/api";
import { useToast } from "@/hooks/use-toast";
import { apiErrorMessage } from "@/lib/api-error";

interface SlackConfig {
  configured: boolean;
  company_id?: string;
  app_id?: string;
  team_id?: string;
  signing_secret?: string;
  bot_user_id?: string;
  enabled?: boolean;
  updated_at?: string;
}

interface SlackUser {
  company_id: string;
  slack_user_id: string;
  label?: string;
  added_at: string;
}

export function SlackTab() {
  const qc = useQueryClient();
  const { toast } = useToast();

  const { data: config, isLoading: configLoading } = useQuery({
    queryKey: ["slack", "config"],
    queryFn: async () => (await api.get<SlackConfig>("/slack")).data,
  });

  const { data: users } = useQuery({
    queryKey: ["slack", "users"],
    queryFn: async () =>
      (await api.get<{ users: SlackUser[] }>("/slack/users")).data.users,
    enabled: !!config?.configured,
  });

  const [appId, setAppId] = useState("");
  const [teamId, setTeamId] = useState("");
  const [botToken, setBotToken] = useState("");
  const [signingSecret, setSigningSecret] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (config?.configured) {
      setAppId(config.app_id ?? "");
      setTeamId(config.team_id ?? "");
      setSigningSecret(config.signing_secret ?? "");
      setEnabled(config.enabled ?? true);
    }
  }, [config]);

  const [userId, setUserId] = useState("");
  const [userLabel, setUserLabel] = useState("");
  const [userError, setUserError] = useState<string | null>(null);

  async function save() {
    setError(null);
    setSaving(true);
    try {
      const body: Record<string, unknown> = {
        app_id: appId.trim(),
        signing_secret: signingSecret.trim(),
        enabled,
      };
      if (botToken.trim()) body.bot_token = botToken.trim();
      if (teamId.trim()) body.team_id = teamId.trim();
      await api.put("/slack", body);
      setBotToken("");
      qc.invalidateQueries({ queryKey: ["slack", "config"] });
      toast({ title: "Slack saved", description: "Configuration updated." });
    } catch (e: unknown) {
      setError(apiErrorMessage(e));
    } finally {
      setSaving(false);
    }
  }

  async function dropConfig() {
    if (!confirm("Remove Slack configuration?")) return;
    try {
      await api.delete("/slack");
      qc.invalidateQueries({ queryKey: ["slack", "config"] });
      qc.invalidateQueries({ queryKey: ["slack", "users"] });
      setAppId("");
      setTeamId("");
      setBotToken("");
      setSigningSecret("");
      setEnabled(true);
      toast({ title: "Slack removed" });
    } catch (e: unknown) {
      toast({
        title: "Remove failed",
        description: apiErrorMessage(e),
        variant: "destructive",
      });
    }
  }

  async function addUser() {
    setUserError(null);
    try {
      await api.post("/slack/users", {
        slack_user_id: userId.trim(),
        label: userLabel.trim() || undefined,
      });
      setUserId("");
      setUserLabel("");
      qc.invalidateQueries({ queryKey: ["slack", "users"] });
    } catch (e: unknown) {
      setUserError(apiErrorMessage(e));
    }
  }

  async function removeUser(id: string) {
    if (!confirm(`Remove ${id}?`)) return;
    await api.delete(`/slack/users/${encodeURIComponent(id)}`);
    qc.invalidateQueries({ queryKey: ["slack", "users"] });
  }

  const isFirstSave = !config?.configured;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle>Slack configuration</CardTitle>
              <CardDescription>
                Credentials from your Slack app. Save here first, then set the Event
                Subscriptions request URL to{" "}
                <code className="text-xs">
                  https://&lt;host&gt;/webhook/slack/events/&lt;app_id&gt;
                </code>
                .
              </CardDescription>
            </div>
            {config?.configured && (
              <Badge variant={config.enabled ? "default" : "secondary"}>
                {config.enabled ? "Enabled" : "Disabled"}
              </Badge>
            )}
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <Label>App ID</Label>
              <Input
                value={appId}
                onChange={(e) => setAppId(e.target.value)}
                placeholder="A01BCDEFGHI"
              />
            </div>
            <div className="space-y-1.5">
              <Label>Workspace ID (optional)</Label>
              <Input
                value={teamId}
                onChange={(e) => setTeamId(e.target.value)}
                placeholder="T01BCDEFGHI"
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>
              Bot user OAuth token{" "}
              {!isFirstSave && (
                <span className="text-xs text-muted-foreground">
                  (leave empty to keep existing)
                </span>
              )}
            </Label>
            <Input
              type="password"
              value={botToken}
              onChange={(e) => setBotToken(e.target.value)}
              placeholder={isFirstSave ? "xoxb-…" : "•••••••••••••"}
            />
          </div>
          <div className="space-y-1.5">
            <Label>Signing secret</Label>
            <Input
              type="password"
              value={signingSecret}
              onChange={(e) => setSigningSecret(e.target.value)}
              placeholder="8f742231b10e…"
            />
          </div>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
              className="h-4 w-4"
            />
            Enabled
          </label>
          {config?.configured && (
            <p className="text-xs text-muted-foreground">
              Bot user ID:{" "}
              {config.bot_user_id ? (
                <code>{config.bot_user_id}</code>
              ) : (
                "detected automatically on the first event Slack delivers"
              )}
            </p>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
          {configLoading && (
            <p className="text-xs text-muted-foreground">Loading…</p>
          )}
        </CardContent>
        <CardFooter className="gap-2">
          <Button
            onClick={save}
            disabled={
              saving ||
              !appId.trim() ||
              !signingSecret.trim() ||
              (isFirstSave && !botToken.trim())
            }
          >
            {saving ? "Saving…" : config?.configured ? "Update" : "Save"}
          </Button>
          {config?.configured && (
            <Button variant="outline" onClick={dropConfig}>
              Remove configuration
            </Button>
          )}
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Allowlist</CardTitle>
          <CardDescription>
            Slack user IDs allowed to chat with the bot. Messages from anyone else are
            silently dropped.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <Label>Slack user ID</Label>
              <Input
                value={userId}
                onChange={(e) => setUserId(e.target.value)}
                placeholder="U01BCDEFGHI"
                disabled={!config?.configured}
              />
            </div>
            <div className="space-y-1.5">
              <Label>Label (optional)</Label>
              <Input
                value={userLabel}
                onChange={(e) => setUserLabel(e.target.value)}
                placeholder="alice"
                disabled={!config?.configured}
              />
            </div>
          </div>
          {userError && <p className="text-sm text-destructive">{userError}</p>}
          <Button
            onClick={addUser}
            disabled={!config?.configured || !userId.trim()}
          >
            Add user
          </Button>
          <div className="divide-y divide-border/50">
            {!config?.configured && (
              <div className="text-sm text-muted-foreground py-4">
                Save configuration first.
              </div>
            )}
            {config?.configured && (users ?? []).length === 0 && (
              <div className="text-sm text-muted-foreground py-4">No users yet.</div>
            )}
            {(users ?? []).map((u) => (
              <div
                key={u.slack_user_id}
                className="flex items-center justify-between py-3"
              >
                <div>
                  <div className="text-sm font-medium">{u.slack_user_id}</div>
                  <div className="text-xs text-muted-foreground">{u.label || "—"}</div>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => removeUser(u.slack_user_id)}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            ))}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
