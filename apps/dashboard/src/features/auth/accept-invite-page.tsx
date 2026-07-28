import { useState } from "react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Sun, Moon } from "lucide-react";
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
import { api } from "@/lib/api";
import { useAuthStore } from "@/store/auth";
import { useThemeStore } from "@/store/theme";
import { apiErrorMessage } from "@/lib/api-error";

const schema = z
  .object({
    // Mirrors the server's validatePassword. Enforcing it here only saves a
    // round trip; the API rejects a short password regardless of what this
    // form allows.
    password: z.string().min(8, "At least 8 characters"),
    confirm: z.string(),
  })
  .refine((v) => v.password === v.confirm, {
    message: "The two passwords do not match",
    path: ["confirm"],
  });
type FormValues = z.infer<typeof schema>;

interface InvitePreview {
  email: string;
  role: string;
  expires_at: string;
}

export function AcceptInvitePage() {
  const navigate = useNavigate();
  const { token } = useSearch({ from: "/accept-invite" });
  const setSession = useAuthStore((s) => s.setSession);
  const { theme, toggle } = useThemeStore();
  const [error, setError] = useState<string | null>(null);

  // Resolving the token before showing the form is what lets an expired link
  // say so up front, rather than after somebody has chosen a password twice.
  const {
    data: invite,
    isLoading,
    isError,
  } = useQuery({
    queryKey: ["invite", token],
    enabled: !!token,
    retry: false,
    queryFn: async () =>
      (await api.get<{ invite: InvitePreview }>("/auth/invite", { params: { token } })).data.invite,
  });

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({ resolver: zodResolver(schema) });

  async function onSubmit(values: FormValues) {
    setError(null);
    try {
      const res = await api.post("/auth/accept-invite", {
        token,
        password: values.password,
      });
      // Accepting logs the invitee straight in: they hold no other credential
      // at this point, and the token they arrived with is now spent.
      setSession(res.data.access_token, res.data.user);
      navigate({ to: "/chat" });
    } catch (e: unknown) {
      setError(apiErrorMessage(e, "Could not accept this invitation"));
    }
  }

  const dead = !token || isError;

  return (
    <div className="min-h-screen flex items-center justify-center px-4 relative">
      <Button
        variant="ghost"
        size="icon"
        onClick={toggle}
        className="absolute top-4 right-4 rounded-full"
        aria-label="Toggle theme"
      >
        {theme === "dark" ? <Sun className="size-5" /> : <Moon className="size-5" />}
      </Button>
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="text-2xl">Join Argentum</CardTitle>
          <p className="text-xs text-muted-foreground -mt-1">by Smartsoft</p>
          {invite && (
            <CardDescription>
              You have been invited as {invite.role === "admin" ? "an admin" : "a member"}. Choose a
              password for {invite.email}.
            </CardDescription>
          )}
        </CardHeader>

        {isLoading && (
          <CardContent>
            <p className="text-sm text-muted-foreground">Checking your invitation…</p>
          </CardContent>
        )}

        {dead && (
          <>
            <CardContent>
              <p className="text-sm text-destructive">
                This invitation is invalid or has expired. Ask an admin on the team to send a new
                one.
              </p>
            </CardContent>
            <CardFooter>
              <Button variant="outline" onClick={() => navigate({ to: "/login" })}>
                Back to sign in
              </Button>
            </CardFooter>
          </>
        )}

        {invite && (
          <form onSubmit={handleSubmit(onSubmit)}>
            <CardContent className="space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="password">Password</Label>
                <Input
                  id="password"
                  type="password"
                  autoComplete="new-password"
                  {...register("password")}
                />
                {errors.password && (
                  <p className="text-xs text-destructive">{errors.password.message}</p>
                )}
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="confirm">Confirm password</Label>
                <Input
                  id="confirm"
                  type="password"
                  autoComplete="new-password"
                  {...register("confirm")}
                />
                {errors.confirm && (
                  <p className="text-xs text-destructive">{errors.confirm.message}</p>
                )}
              </div>
              {error && <p className="text-sm text-destructive">{error}</p>}
            </CardContent>
            <CardFooter>
              <Button type="submit" disabled={isSubmitting}>
                {isSubmitting ? "Setting up…" : "Accept invitation"}
              </Button>
            </CardFooter>
          </form>
        )}
      </Card>
    </div>
  );
}
