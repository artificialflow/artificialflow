import { useState, type FormEvent } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { consoleLogin, ConsoleLoginError, type IdentityPrincipal } from "@/lib/api";

type ConsoleLoginProps = {
  onSignedIn: (principal: IdentityPrincipal) => void;
};

export default function ConsoleLogin({ onSignedIn }: ConsoleLoginProps) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      onSignedIn(await consoleLogin(username, password));
    } catch (err) {
      setError(err instanceof ConsoleLoginError ? err.message : "Unable to reach ArtificialFlow");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex h-screen items-center justify-center bg-muted/50 p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-2xl">ArtificialFlow</CardTitle>
          <p className="text-sm text-muted-foreground">Sign in to the console</p>
        </CardHeader>
        <CardContent>
          <form className="space-y-4" onSubmit={(event) => void submit(event)}>
            <div className="space-y-2">
              <Label htmlFor="console-username">Username</Label>
              <Input
                id="console-username"
                autoComplete="username"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="console-password">Password</Label>
              <Input
                id="console-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                required
              />
            </div>
            {error ? <p className="text-sm text-destructive">{error}</p> : null}
            <Button type="submit" className="w-full" disabled={submitting}>
              {submitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
              Sign in
            </Button>
            <p className="text-xs text-muted-foreground">
              IAM is disabled: this login only gates the console. The API accepts requests without a token.
            </p>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
