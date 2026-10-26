export const RUNTIME_UI_CAPABILITIES = Object.freeze({
  decisions: true,
  incidents: true,
});

export type RuntimeUiCapability = keyof typeof RUNTIME_UI_CAPABILITIES;

export function hasRuntimeUiCapability(capability: RuntimeUiCapability): boolean {
  return RUNTIME_UI_CAPABILITIES[capability];
}
