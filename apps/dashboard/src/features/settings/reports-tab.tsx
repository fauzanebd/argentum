import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, Trash2, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";
import { contrastOnWhite, parseHex } from "@/lib/contrast";
import { useToast } from "@/hooks/use-toast";

interface Branding {
  logo_key?: string;
  primary_color?: string;
  footer_text?: string;
  legal_name?: string;
  locale?: string;
  confidentiality_label?: string;
  show_argentum_credit?: boolean;
}

interface BrandingResponse {
  branding: Branding;
  defaults: { primary_color: string; company_name: string; locale: string };
  limits: { min_contrast: number; max_logo_bytes: number; max_logo_edge: number };
}

export function ReportsTab() {
  const qc = useQueryClient();
  const { toast } = useToast();

  const { data, isLoading } = useQuery<BrandingResponse>({
    queryKey: ["report-branding"],
    queryFn: async () => (await api.get("/reports/branding")).data,
  });

  const [form, setForm] = useState<Branding>({});
  const [previewURL, setPreviewURL] = useState<string | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  // The form is seeded once from the server and then owned by the browser:
  // re-seeding on every refetch would discard whatever the customer had typed
  // while a preview render was in flight.
  const seeded = useRef(false);
  useEffect(() => {
    if (data && !seeded.current) {
      setForm(data.branding ?? {});
      seeded.current = true;
    }
  }, [data]);

  const minContrast = data?.limits.min_contrast ?? 3;
  const defaultColor = data?.defaults.primary_color ?? "#F25C5C";
  const color = form.primary_color ?? "";
  const ratio = color ? contrastOnWhite(color) : null;
  const colorValid = color === "" || (parseHex(color) !== null && (ratio ?? 0) >= minContrast);

  function patch(next: Partial<Branding>) {
    setForm((f) => ({ ...f, ...next }));
  }

  const save = useMutation({
    mutationFn: async () => (await api.put("/reports/branding", form)).data as BrandingResponse,
    onSuccess: (res) => {
      setForm(res.branding ?? {});
      qc.invalidateQueries({ queryKey: ["report-branding"] });
      toast({ title: "Branding saved", description: "The next report generated will use it." });
    },
    onError: (e) =>
      toast({ title: "Could not save", description: apiErrorMessage(e), variant: "destructive" }),
  });

  const upload = useMutation({
    mutationFn: async (file: File) => {
      const body = new FormData();
      body.append("logo", file);
      const res = await api.post("/reports/branding/logo", body);
      return res.data.logo_key as string;
    },
    onSuccess: (key) => {
      patch({ logo_key: key });
      toast({ title: "Logo uploaded", description: "Save to apply it to your reports." });
    },
    onError: (e) =>
      toast({ title: "Logo rejected", description: apiErrorMessage(e), variant: "destructive" }),
  });

  // The preview renders the branding currently in the form, saved or not —
  // that is what makes it worth having. It is a PDF in an <iframe> rather than
  // a rendered-in-JS approximation, so what is on screen is the renderer's own
  // output and not a second opinion about it.
  const preview = useMutation({
    mutationFn: async () => {
      const res = await api.post("/reports/preview", form, { responseType: "blob" });
      return res.data as Blob;
    },
    onSuccess: (blob) => {
      setPreviewURL((old) => {
        if (old) URL.revokeObjectURL(old);
        return URL.createObjectURL(blob);
      });
    },
    onError: async (e: unknown) => {
      // A failed preview comes back as a Blob because the request asked for
      // one, so the JSON error inside it has to be read out before it can be
      // shown. Without this the customer gets "[object Blob]".
      let message = apiErrorMessage(e);
      const body = (e as { response?: { data?: unknown } })?.response?.data;
      if (body instanceof Blob) {
        try {
          const parsed = JSON.parse(await body.text());
          if (typeof parsed?.error === "string") message = parsed.error;
        } catch {
          /* not JSON — keep the generic message */
        }
      }
      toast({ title: "Preview failed", description: message, variant: "destructive" });
    },
  });

  // Revoke the last object URL when the tab goes away; a blob URL keeps the
  // whole PDF alive in memory until it is released.
  useEffect(() => {
    return () => {
      setPreviewURL((old) => {
        if (old) URL.revokeObjectURL(old);
        return null;
      });
    };
  }, []);

  if (isLoading) return null;

  const credit = form.show_argentum_credit ?? true;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Report branding</CardTitle>
          <CardDescription>
            Applied to every PDF and deck Argentum generates for your company. Anything left
            blank falls back to the Argentum default on its own — a logo with no accent colour
            gets your logo and our red.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          <div className="space-y-1.5">
            <Label>Logo</Label>
            <div className="flex items-center gap-3">
              {form.logo_key ? (
                <span className="text-xs font-mono text-muted-foreground">{form.logo_key}</span>
              ) : (
                <span className="text-xs text-muted-foreground">
                  No logo — documents carry your company name as a wordmark.
                </span>
              )}
            </div>
            <div className="flex items-center gap-2 pt-1">
              <input
                ref={fileInput}
                type="file"
                accept="image/png,image/jpeg"
                className="hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) upload.mutate(file);
                  e.target.value = "";
                }}
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => fileInput.current?.click()}
                disabled={upload.isPending}
              >
                <Upload className="h-3.5 w-3.5 mr-1.5" />
                {upload.isPending ? "Uploading…" : "Upload logo"}
              </Button>
              {form.logo_key && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => patch({ logo_key: "" })}
                >
                  <Trash2 className="h-3.5 w-3.5 mr-1.5" />
                  Remove
                </Button>
              )}
            </div>
            <p className="text-xs text-muted-foreground">
              PNG or JPEG, up to {Math.round((data?.limits.max_logo_bytes ?? 524288) / 1024)} KB.
              Larger than {data?.limits.max_logo_edge ?? 2000}px is scaled down, and every upload
              is re-encoded as PNG.
            </p>
          </div>

          <div className="space-y-1.5 max-w-sm">
            <Label>Accent colour</Label>
            <div className="flex items-center gap-2">
              <input
                type="color"
                aria-label="Accent colour"
                value={parseHex(color) ? color : defaultColor}
                onChange={(e) => patch({ primary_color: e.target.value.toUpperCase() })}
                className="h-9 w-12 rounded border border-border bg-transparent p-1"
              />
              <Input
                value={color}
                placeholder={defaultColor}
                onChange={(e) => patch({ primary_color: e.target.value.toUpperCase() })}
                className="font-mono"
              />
            </div>
            <p className={`text-xs ${colorValid ? "text-muted-foreground" : "text-destructive"}`}>
              {color === ""
                ? `Blank uses the Argentum red, ${defaultColor}.`
                : ratio === null
                  ? "Enter a colour as #RRGGBB."
                  : `${ratio.toFixed(2)}:1 against white — ${
                      ratio >= minContrast
                        ? "readable on paper"
                        : `too light, needs at least ${minContrast.toFixed(1)}:1`
                    }.`}
            </p>
            <p className="text-xs text-muted-foreground">
              Used for section rules, the cover and headings. Chart colours do not change: that
              palette is checked as a set for greyscale printing and colour-vision deficiency.
            </p>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>Legal name</Label>
              <Input
                value={form.legal_name ?? ""}
                placeholder={data?.defaults.company_name ?? ""}
                onChange={(e) => patch({ legal_name: e.target.value })}
              />
              <p className="text-xs text-muted-foreground">
                Goes on the cover and in the document's author property.
              </p>
            </div>

            <div className="space-y-1.5">
              <Label>Default document language</Label>
              <Select
                value={form.locale ?? ""}
                onValueChange={(v) => patch({ locale: v === "auto" ? "" : v })}
              >
                <SelectTrigger>
                  <SelectValue placeholder="From currency" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="auto">From currency</SelectItem>
                  <SelectItem value="en">English</SelectItem>
                  <SelectItem value="id">Bahasa Indonesia</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                Sets "Page 2 of 17" against "Halaman 2 dari 17", and the date format with it.
              </p>
            </div>

            <div className="space-y-1.5">
              <Label>Confidentiality label</Label>
              <Input
                value={form.confidentiality_label ?? ""}
                placeholder="Internal"
                onChange={(e) => patch({ confidentiality_label: e.target.value })}
              />
              <p className="text-xs text-muted-foreground">
                Shown in the footer of documents that do not set their own.
              </p>
            </div>

            <div className="space-y-1.5">
              <Label>Footer line</Label>
              <Input
                value={form.footer_text ?? ""}
                placeholder="© 2026 Your Company. All rights reserved."
                onChange={(e) => patch({ footer_text: e.target.value })}
              />
              <p className="text-xs text-muted-foreground">
                Carried on every page and every slide.
              </p>
            </div>
          </div>

          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              checked={credit}
              onChange={(e) => patch({ show_argentum_credit: e.target.checked })}
              className="mt-0.5"
            />
            <span>
              Show "Made with Argentum" in the footer
              <span className="block text-xs text-muted-foreground">
                Off removes our name from the document entirely.
              </span>
            </span>
          </label>
        </CardContent>
        <CardFooter className="gap-2">
          <Button onClick={() => save.mutate()} disabled={save.isPending || !colorValid}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
          <Button
            variant="outline"
            onClick={() => preview.mutate()}
            disabled={preview.isPending || !colorValid}
          >
            <RefreshCw className={`h-3.5 w-3.5 mr-1.5 ${preview.isPending ? "animate-spin" : ""}`} />
            {previewURL ? "Refresh preview" : "Preview"}
          </Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Live preview</CardTitle>
          <CardDescription>
            A sample report rendered by the same code that renders your real ones, with the
            settings above — saved or not. The figures in it are invented.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {previewURL ? (
            <iframe
              title="Branding preview"
              src={previewURL}
              className="w-full h-[640px] rounded border border-border bg-white"
            />
          ) : (
            <div className="h-40 rounded border border-dashed border-border grid place-items-center text-sm text-muted-foreground">
              Press Preview to render a sample report.
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
