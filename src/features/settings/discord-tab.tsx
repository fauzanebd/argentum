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

interface DiscordConfig {
  configured: boolean;
  company_id?: string;
  application_id?: string;
  public_key?: string;
  guild_id?: string;
  enabled?: boolean;
  updated_at?: string;
}

interface DiscordUser {
  company_id: string;
  discord_user_id: string;
  label?: string;
  added_at: string;
}

export function DiscordTab() {
  const qc = useQueryClient();
  const { toast } = useToast();

  const { data: config, isLoading: configLoading } = useQuery({
    queryKey: ["discord", "config"],
    queryFn: async () => (await api.get<DiscordConfig>("/discord")).data,
  });

  const { data: users } = useQuery({
    queryKey: ["discord", "users"],
    queryFn: async () =>
      (await api.get<{ users: DiscordUser[] }>("/discord/users")).data.users,
    enabled: !!config?.configured,
  });

  const [applicationId, setApplicationId] = useState("");
  const [publicKey, setPublicKey] = useState("");
  const [botToken, setBotToken] = useState("");
  const [guildId, setGuildId] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (config?.configured) {
      setApplicationId(config.application_id ?? "");
      setPublicKey(config.public_key ?? "");
      setGuildId(config.guild_id ?? "");
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
        application_id: applicationId.trim(),
        public_key: publicKey.trim(),
        enabled,
      };
      if (botToken.trim()) body.bot_token = botToken.trim();
      if (guildId.trim()) body.guild_id = guildId.trim();
      await api.put("/discord", body);
      setBotToken("");
      qc.invalidateQueries({ queryKey: ["discord", "config"] });
      toast({ title: "Discord saved", description: "Configuration updated." });
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message);
    } finally {
      setSaving(false);
    }
  }

  async function dropConfig() {
    if (!confirm("Remove Discord configuration? Gateway session will close.")) return;
    try {
      await api.delete("/discord");
      qc.invalidateQueries({ queryKey: ["discord", "config"] });
      qc.invalidateQueries({ queryKey: ["discord", "users"] });
      setApplicationId("");
      setPublicKey("");
      setBotToken("");
      setGuildId("");
      setEnabled(true);
      toast({ title: "Discord removed" });
    } catch (e: any) {
      toast({
        title: "Remove failed",
        description: e?.response?.data?.error || e.message,
        variant: "destructive",
      });
    }
  }

  async function addUser() {
    setUserError(null);
    try {
      await api.post("/discord/users", {
        discord_user_id: userId.trim(),
        label: userLabel.trim() || undefined,
      });
      setUserId("");
      setUserLabel("");
      qc.invalidateQueries({ queryKey: ["discord", "users"] });
    } catch (e: any) {
      setUserError(e?.response?.data?.error || e.message);
    }
  }

  async function removeUser(id: string) {
    if (!confirm(`Remove ${id}?`)) return;
    await api.delete(`/discord/users/${encodeURIComponent(id)}`);
    qc.invalidateQueries({ queryKey: ["discord", "users"] });
  }

  const isFirstSave = !config?.configured;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle>Discord configuration</CardTitle>
              <CardDescription>
                Credentials from the Discord Developer Portal. Save credentials here first, then
                set the Interactions Endpoint URL to{" "}
                <code className="text-xs">https://&lt;host&gt;/webhook/discord/interactions</code>.
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
              <Label>Application ID</Label>
              <Input
                value={applicationId}
                onChange={(e) => setApplicationId(e.target.value)}
                placeholder="1234567890"
              />
            </div>
            <div className="space-y-1.5">
              <Label>Guild ID (optional)</Label>
              <Input
                value={guildId}
                onChange={(e) => setGuildId(e.target.value)}
                placeholder="9876543210"
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>Public key (Ed25519 hex)</Label>
            <Input
              value={publicKey}
              onChange={(e) => setPublicKey(e.target.value)}
              placeholder="ed25519-hex..."
            />
          </div>
          <div className="space-y-1.5">
            <Label>
              Bot token{" "}
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
              placeholder={isFirstSave ? "MTAxNzQ..." : "•••••••••••••"}
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
              !applicationId.trim() ||
              !publicKey.trim() ||
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
            Discord user IDs (snowflakes) allowed to chat with the bot. Messages from anyone else
            are silently dropped.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <Label>Discord user ID</Label>
              <Input
                value={userId}
                onChange={(e) => setUserId(e.target.value)}
                placeholder="234567890123456789"
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
                key={u.discord_user_id}
                className="flex items-center justify-between py-3"
              >
                <div>
                  <div className="text-sm font-medium">{u.discord_user_id}</div>
                  <div className="text-xs text-muted-foreground">{u.label || "—"}</div>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => removeUser(u.discord_user_id)}
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
