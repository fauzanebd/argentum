import { useState } from "react";
import { useNavigate, Link } from "@tanstack/react-router";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Sun, Moon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import { useAuthStore } from "@/store/auth";
import { useThemeStore } from "@/store/theme";
import { apiErrorMessage } from "@/lib/api-error";

const schema = z.object({
  email: z.string().email(),
  password: z.string().min(1),
});
type FormValues = z.infer<typeof schema>;

// Public registration is closed, but testers still need a way in. Flipping the
// theme toggle more than this many times on the login screen reveals the
// sign-up link. Deliberately undiscoverable by accident; remove along with the
// counter below once registration opens for real.
const THEME_TOGGLES_TO_REVEAL_SIGNUP = 6;

export function LoginPage() {
  const navigate = useNavigate();
  const setSession = useAuthStore((s) => s.setSession);
  const { theme, toggle } = useThemeStore();
  const [error, setError] = useState<string | null>(null);
  // Per-visit, not persisted: reloading the page hides the link again.
  const [themeToggles, setThemeToggles] = useState(0);
  const signupRevealed = themeToggles > THEME_TOGGLES_TO_REVEAL_SIGNUP;

  function onToggleTheme() {
    toggle();
    setThemeToggles((n) => n + 1);
  }

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({ resolver: zodResolver(schema) });

  async function onSubmit(values: FormValues) {
    setError(null);
    try {
      const res = await api.post("/auth/login", values);
      setSession(res.data.access_token, res.data.user);
      navigate({ to: "/chat" });
    } catch (e: unknown) {
      setError(apiErrorMessage(e, "Login failed"));
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center px-4 relative">
      {/* Theme toggle */}
      <Button
        variant="ghost"
        size="icon"
        onClick={onToggleTheme}
        className="absolute top-4 right-4 rounded-full"
        aria-label="Toggle theme"
      >
        {theme === "dark" ? <Sun className="size-5" /> : <Moon className="size-5" />}
      </Button>
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="text-2xl">Sign in to Argentum</CardTitle>
          <p className="text-xs text-muted-foreground -mt-1">by Smartsoft</p>
          <CardDescription>Welcome back. Enter your credentials below.</CardDescription>
        </CardHeader>
        <form onSubmit={handleSubmit(onSubmit)}>
          <CardContent className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="email">Email</Label>
              <Input id="email" type="email" autoComplete="email" {...register("email")} />
              {errors.email && <p className="text-xs text-destructive">{errors.email.message}</p>}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="password">Password</Label>
              <Input id="password" type="password" autoComplete="current-password" {...register("password")} />
              {errors.password && <p className="text-xs text-destructive">{errors.password.message}</p>}
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
          </CardContent>
          <CardFooter className="flex-col gap-3 items-stretch">
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting ? "Signing in..." : "Sign in"}
            </Button>
            {/* Hidden until the theme toggle above is flipped enough times. */}
            {signupRevealed && (
              <p className="text-sm text-center text-muted-foreground">
                Don't have an account?{" "}
                <Link to="/signup" className="text-foreground underline">
                  Sign up
                </Link>
              </p>
            )}
          </CardFooter>
        </form>
      </Card>
    </div>
  );
}
