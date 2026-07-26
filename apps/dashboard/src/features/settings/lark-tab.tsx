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

interface LarkConfig {
  configured: boolean;
  company_id?: string;
  app_id?: string;
  verification_token?: string;
  encrypt_key?: string;
  bot_open_id?: string;
  enabled?: boolean;
  updated_at?: string;
}

interface LarkUser {
  company_id: string;
  lark_open_id: string;
  label?: string;
  added_at: string;
}

export function LarkTab() {
  const qc = useQueryClient();
  const { toast } = useToast();

  const { data: config, isLoading: configLoading } = useQuery({
    queryKey: ["lark", "config"],
    queryFn: async () => (await api.get<LarkConfig>("/lark")).data,
  });

  const { data: users } = useQuery({
    queryKey: ["lark", "users"],
    queryFn: async () =>
      (await api.get<{ users: LarkUser[] }>("/lark/users")).data.users,
    enabled: !!config?.configured,
  });

  const [appId, setAppId] = useState("");
  const [appSecret, setAppSecret] = useState("");
  const [verificationToken, setVerificationToken] = useState("");
  const [encryptKey, setEncryptKey] = useState("");
  const [botOpenId, setBotOpenId] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (config?.configured) {
      setAppId(config.app_id ?? "");
      setVerificationToken(config.verification_token ?? "");
      setEncryptKey(config.encrypt_key ?? "");
      setBotOpenId(config.bot_open_id ?? "");
      setEnabled(config.enabled ?? true);
    }
  }, [config]);

  const [openId, setOpenId] = useState("");
  const [userLabel, setUserLabel] = useState("");
  const [userError, setUserError] = useState<string | null>(null);

  async function save() {
    setError(null);
    setSaving(true);
    try {
      const body: Record<string, unknown> = {
        app_id: appId.trim(),
        verification_token: verificationToken.trim(),
        enabled,
      };
      if (appSecret.trim()) body.app_secret = appSecret.trim();
      if (encryptKey.trim()) body.encrypt_key = encryptKey.trim();
      if (botOpenId.trim()) body.bot_open_id = botOpenId.trim();
      await api.put("/lark", body);
      setAppSecret("");
      qc.invalidateQueries({ queryKey: ["lark", "config"] });
      toast({ title: "Lark saved", description: "Configuration updated." });
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message);
    } finally {
      setSaving(false);
    }
  }

  async function dropConfig() {
    if (!confirm("Remove Lark configuration?")) return;
    try {
      await api.delete("/lark");
      qc.invalidateQueries({ queryKey: ["lark", "config"] });
      qc.invalidateQueries({ queryKey: ["lark", "users"] });
      setAppId("");
      setAppSecret("");
      setVerificationToken("");
      setEncryptKey("");
      setBotOpenId("");
      setEnabled(true);
      toast({ title: "Lark removed" });
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
      await api.post("/lark/users", {
        lark_open_id: openId.trim(),
        label: userLabel.trim() || undefined,
      });
      setOpenId("");
      setUserLabel("");
      qc.invalidateQueries({ queryKey: ["lark", "users"] });
    } catch (e: any) {
      setUserError(e?.response?.data?.error || e.message);
    }
  }

  async function removeUser(id: string) {
    if (!confirm(`Remove ${id}?`)) return;
    await api.delete(`/lark/users/${encodeURIComponent(id)}`);
    qc.invalidateQueries({ queryKey: ["lark", "users"] });
  }

  const isFirstSave = !config?.configured;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle>Lark configuration</CardTitle>
              <CardDescription>
                Credentials from the Lark Open Platform. Save here first, then set the event
                subscription request URL to{" "}
                <code className="text-xs">
                  https://&lt;host&gt;/webhook/lark/events/&lt;app_id&gt;
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
                placeholder="cli_a1b2c3d4"
              />
            </div>
            <div className="space-y-1.5">
              <Label>Bot open_id (optional)</Label>
              <Input
                value={botOpenId}
                onChange={(e) => setBotOpenId(e.target.value)}
                placeholder="ou_xxx"
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>
              App secret{" "}
              {!isFirstSave && (
                <span className="text-xs text-muted-foreground">
                  (leave empty to keep existing)
                </span>
              )}
            </Label>
            <Input
              type="password"
              value={appSecret}
              onChange={(e) => setAppSecret(e.target.value)}
              placeholder={isFirstSave ? "secret-..." : "•••••••••••••"}
            />
          </div>
          <div className="space-y-1.5">
            <Label>Verification token</Label>
            <Input
              value={verificationToken}
              onChange={(e) => setVerificationToken(e.target.value)}
              placeholder="v-tok-..."
            />
          </div>
          <div className="space-y-1.5">
            <Label>
              Encrypt key{" "}
              <span className="text-xs text-muted-foreground">(recommended)</span>
            </Label>
            <Input
              type="password"
              value={encryptKey}
              onChange={(e) => setEncryptKey(e.target.value)}
              placeholder="enc-key-..."
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
          {!isFirstSave && !config?.bot_open_id && !botOpenId.trim() && (
            <p className="text-xs text-muted-foreground">
              Bot open_id missing — inbound @mentions are ignored until set. Send a test message
              and copy the bot's open_id from server logs.
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
              !verificationToken.trim() ||
              (isFirstSave && !appSecret.trim())
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
            Lark open_ids allowed to chat with the bot. Messages from anyone else are silently
            dropped.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <Label>Lark open_id</Label>
              <Input
                value={openId}
                onChange={(e) => setOpenId(e.target.value)}
                placeholder="ou_abc123"
                disabled={!config?.configured}
              />
            </div>
            <div className="space-y-1.5">
              <Label>Label (optional)</Label>
              <Input
                value={userLabel}
                onChange={(e) => setUserLabel(e.target.value)}
                placeholder="bob"
                disabled={!config?.configured}
              />
            </div>
          </div>
          {userError && <p className="text-sm text-destructive">{userError}</p>}
          <Button
            onClick={addUser}
            disabled={!config?.configured || !openId.trim()}
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
                key={u.lark_open_id}
                className="flex items-center justify-between py-3"
              >
                <div>
                  <div className="text-sm font-medium">{u.lark_open_id}</div>
                  <div className="text-xs text-muted-foreground">{u.label || "—"}</div>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => removeUser(u.lark_open_id)}
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
