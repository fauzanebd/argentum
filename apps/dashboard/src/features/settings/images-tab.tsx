import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ImagePlus, Trash2, Upload } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";
import { useObjectUrl } from "@/hooks/use-object-url";
import { useToast } from "@/hooks/use-toast";
import { useIsAdmin } from "@/store/auth";

/**
 * The workspace's picture library (T-G12).
 *
 * These are the photographs the agent draws onto a promotion card, and the
 * screen exists because **the name is the interface**: the model asks for
 * "jeruk cara cara" and the backend resolves that against this list, exactly.
 * So the name field is the important control here, not the file picker — an
 * image nobody can name is an image the agent cannot use, and a name nobody
 * would type is the same thing one step later.
 *
 * Members see the library and cannot change it. That is the 2026-08-04 rule
 * this codebase keeps: a disabled control tells a member who to ask, where a
 * hidden one tells them the feature does not exist.
 */

interface PostImage {
  id: string;
  name: string;
  alt?: string;
  width: number;
  height: number;
  byte_size: number;
  created_at: string;
}

interface ImagesResponse {
  images: PostImage[];
  limits: {
    max_bytes: number;
    max_edge: number;
    max_name_chars: number;
    max_alt_chars: number;
  };
}

export function ImagesTab() {
  const qc = useQueryClient();
  const { toast } = useToast();
  const isAdmin = useIsAdmin();

  const { data, isLoading } = useQuery<ImagesResponse>({
    queryKey: ["post-images"],
    queryFn: async () => (await api.get("/post-images")).data,
  });

  const [file, setFile] = useState<File | null>(null);
  const [name, setName] = useState("");
  const [alt, setAlt] = useState("");
  const fileInput = useRef<HTMLInputElement>(null);

  const maxBytes = data?.limits.max_bytes ?? 4 << 20;
  const maxName = data?.limits.max_name_chars ?? 80;

  function reset() {
    setFile(null);
    setName("");
    setAlt("");
    if (fileInput.current) fileInput.current.value = "";
  }

  function pick(f: File | null) {
    setFile(f);
    // The filename is a suggestion, not the name: somebody uploading
    // `jeruk-cara-cara.jpg` has already named the thing, and making them type
    // it again is the sort of friction that ends with images called "IMG_2831".
    if (f && !name) setName(f.name.replace(/\.[a-z0-9]{1,5}$/i, "").replace(/[-_]+/g, " "));
  }

  const upload = useMutation({
    mutationFn: async () => {
      if (!file) throw new Error("no file");
      const body = new FormData();
      body.append("image", file);
      body.append("name", name.trim());
      body.append("alt", alt.trim());
      return (await api.post("/post-images", body)).data as PostImage;
    },
    onSuccess: (img) => {
      reset();
      qc.invalidateQueries({ queryKey: ["post-images"] });
      toast({
        title: "Image added",
        description: `Ask for it by name: “${img.name}”.`,
      });
    },
    onError: (e) =>
      toast({ title: "Could not add it", description: apiErrorMessage(e), variant: "destructive" }),
  });

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/post-images/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["post-images"] });
      toast({ title: "Image removed" });
    },
    onError: (e) =>
      toast({ title: "Could not remove it", description: apiErrorMessage(e), variant: "destructive" }),
  });

  const tooBig = file != null && file.size > maxBytes;
  const nameTooLong = name.trim().length > maxName;
  const canUpload = isAdmin && file != null && name.trim() !== "" && !tooBig && !nameTooLong;

  const images = data?.images ?? [];

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Images</CardTitle>
          <CardDescription>
            Product photographs the assistant can put on a promotion post. Ask for one by its
            name in chat — “buatkan promo diskon jeruk cara cara” — and the name here is what
            it looks for, so name them the way you would say them.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="image-file">File</Label>
              <Input
                id="image-file"
                ref={fileInput}
                type="file"
                accept="image/png,image/jpeg"
                disabled={!isAdmin || upload.isPending}
                onChange={(e) => pick(e.target.files?.[0] ?? null)}
              />
              <p className="text-xs text-muted-foreground">
                PNG or JPEG, up to {Math.round(maxBytes / (1 << 20))} MB. Larger pictures are
                resized rather than refused.
              </p>
              {tooBig ? (
                <p className="text-xs text-destructive">
                  This file is {(file!.size / (1 << 20)).toFixed(1)} MB.
                </p>
              ) : null}
            </div>

            <div className="space-y-2">
              <Label htmlFor="image-name">Name</Label>
              <Input
                id="image-name"
                value={name}
                disabled={!isAdmin || upload.isPending}
                placeholder="Jeruk Cara Cara"
                onChange={(e) => setName(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                What the assistant asks for. One per workspace, and matched exactly.
              </p>
              {nameTooLong ? (
                <p className="text-xs text-destructive">
                  {name.trim().length} characters; the limit is {maxName}.
                </p>
              ) : null}
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="image-alt">Description</Label>
            <Input
              id="image-alt"
              value={alt}
              disabled={!isAdmin || upload.isPending}
              placeholder="Jeruk cara cara utuh dan dibelah dua"
              onChange={(e) => setAlt(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Optional, and it travels onto the post: it is what a screen reader reads and what
              a publishing tool fills its caption field with.
            </p>
          </div>

          <div className="flex items-center gap-3">
            <Button onClick={() => upload.mutate()} disabled={!canUpload || upload.isPending}>
              <Upload className="mr-2 h-4 w-4" />
              {upload.isPending ? "Adding…" : "Add image"}
            </Button>
            {!isAdmin ? (
              <p className="text-xs text-muted-foreground">
                Only an admin can add or remove images. You can use any of them by name in chat.
              </p>
            ) : null}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>In this workspace</CardTitle>
          <CardDescription>
            {isLoading
              ? "Loading…"
              : images.length === 0
                ? "No images yet. A promotion post without one is drawn as type on a coloured card."
                : `${images.length} image${images.length === 1 ? "" : "s"}.`}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {images.length === 0 && !isLoading ? (
            <div className="flex flex-col items-center gap-2 py-8 text-center text-sm text-muted-foreground">
              <ImagePlus className="h-8 w-8" aria-hidden />
              <p>Add a product photograph above to use it on a promotion.</p>
            </div>
          ) : (
            <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {images.map((img) => (
                <LibraryCard
                  key={img.id}
                  image={img}
                  canDelete={isAdmin}
                  deleting={remove.isPending}
                  onDelete={() => remove.mutate(img.id)}
                />
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function LibraryCard({
  image,
  canDelete,
  deleting,
  onDelete,
}: {
  image: PostImage;
  canDelete: boolean;
  deleting: boolean;
  onDelete: () => void;
}) {
  const { url, failed } = useObjectUrl(`/post-images/${image.id}/content`);

  return (
    <li className="overflow-hidden rounded-lg border border-border">
      <div className="flex h-40 items-center justify-center bg-muted/40">
        {url ? (
          <img
            src={url}
            alt={image.alt || image.name}
            className="h-full w-full object-contain"
            loading="lazy"
          />
        ) : failed ? (
          <span className="px-3 text-center text-xs text-muted-foreground">
            {image.alt || image.name}
          </span>
        ) : (
          <div className="h-full w-full animate-pulse bg-muted" aria-hidden />
        )}
      </div>
      <div className="space-y-1 p-3">
        <p className="truncate text-sm font-medium" title={image.name}>
          {image.name}
        </p>
        <p className="text-xs text-muted-foreground">
          {image.width}×{image.height} · {(image.byte_size / 1024).toFixed(0)} KB
        </p>
        {canDelete ? (
          <Button
            variant="ghost"
            size="sm"
            className="mt-1 h-7 px-2 text-destructive hover:text-destructive"
            disabled={deleting}
            onClick={onDelete}
          >
            <Trash2 className="mr-1 h-3.5 w-3.5" />
            Remove
          </Button>
        ) : null}
      </div>
    </li>
  );
}
