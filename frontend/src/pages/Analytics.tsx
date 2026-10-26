import { BarChart3 } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ARTIFICIALFLOW_ANALYTICS_ROLE } from "@/lib/roles";

export default function Analytics() {
  return (
    <div className="space-y-6">
      <div className="flex items-center gap-3">
        <BarChart3 className="h-6 w-6" />
        <h1 className="text-2xl font-bold">Analytics</h1>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Coming soon</CardTitle>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          Analytics features for the {ARTIFICIALFLOW_ANALYTICS_ROLE} role are not available yet.
        </CardContent>
      </Card>
    </div>
  );
}
