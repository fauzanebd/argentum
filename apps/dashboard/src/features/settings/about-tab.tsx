import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export function AboutTab() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>About Argentum</CardTitle>
        <CardDescription>A Smartsoft product.</CardDescription>
      </CardHeader>
      <CardContent className="text-sm text-muted-foreground">
        Argentum is built and maintained by Smartsoft.
      </CardContent>
    </Card>
  );
}
