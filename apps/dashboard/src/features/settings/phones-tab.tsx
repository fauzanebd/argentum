import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";

interface Phone {
  company_id: string;
  phone_number: string;
  label?: string;
  added_at: string;
}

export function PhonesTab() {
  const qc = useQueryClient();
  const [phone, setPhone] = useState("");
  const [label, setLabel] = useState("");
  const [error, setError] = useState<string | null>(null);

  const { data: phones } = useQuery({
    queryKey: ["phones"],
    queryFn: async () => (await api.get<{ phones: Phone[] }>("/phones")).data.phones,
  });

  async function add() {
    setError(null);
    try {
      await api.post("/phones", { phone_number: phone, label });
      setPhone("");
      setLabel("");
      qc.invalidateQueries({ queryKey: ["phones"] });
    } catch (e: unknown) {
      setError(apiErrorMessage(e));
    }
  }

  async function remove(p: string) {
    if (!confirm(`Remove ${p}?`)) return;
    await api.delete(`/phones/${encodeURIComponent(p)}`);
    qc.invalidateQueries({ queryKey: ["phones"] });
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Authorise a phone number</CardTitle>
          <CardDescription>
            WhatsApp messages from this number will be linked to your company. Numbers are unique
            across all of Argentum.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <Label>Phone number (E.164)</Label>
              <Input value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="+6281234567890" />
            </div>
            <div className="space-y-1.5">
              <Label>Label (optional)</Label>
              <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Owner / Sales lead" />
            </div>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
        <CardFooter>
          <Button onClick={add} disabled={!phone}>
            Add phone number
          </Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Authorised numbers</CardTitle>
        </CardHeader>
        <CardContent className="divide-y divide-border/50">
          {(phones ?? []).length === 0 && (
            <div className="text-sm text-muted-foreground py-4">No phone numbers yet.</div>
          )}
          {(phones ?? []).map((p) => (
            <div key={p.phone_number} className="flex items-center justify-between py-3">
              <div>
                <div className="text-sm font-medium">{p.phone_number}</div>
                <div className="text-xs text-muted-foreground">{p.label || "—"}</div>
              </div>
              <Button variant="ghost" size="icon" onClick={() => remove(p.phone_number)}>
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
