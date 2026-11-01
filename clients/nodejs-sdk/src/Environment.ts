export type ArtificialFlowEnvironmentSource = Record<string, string | undefined>;

export interface ArtificialFlowEnvironmentConfig {
    baseUrl: string;
    token: string;
    workflowKey: string;
    businessKey: string;
    messageName: string;
    messageCorrelationKey: string;
    workerJobType: string;
}

export function readArtificialFlowEnvironmentValue(
    environment: ArtificialFlowEnvironmentSource,
    name: string,
    fallback = '',
): string {
    return environment[name]?.trim() || fallback;
}

export function resolveArtificialFlowEnvironment(
    environment: ArtificialFlowEnvironmentSource =
        typeof process === 'undefined' ? {} : process.env,
    now: () => number = Date.now,
): ArtificialFlowEnvironmentConfig {
    const read = (name: string, fallback = '') =>
        readArtificialFlowEnvironmentValue(environment, name, fallback);

    return {
        baseUrl:
            environment.ARTIFICIALFLOW_BASE_URL?.trim()
            || environment.ARTIFICIALFLOW_API_URL?.trim()
            || 'http://localhost:9100/api',
        token: read('ARTIFICIALFLOW_TOKEN'),
        workflowKey: read(
            'ARTIFICIALFLOW_WORKFLOW_KEY',
            '<WORKFLOW_DEFINITION_KEY_OR_ID_TO_START>',
        ),
        businessKey: read(
            'ARTIFICIALFLOW_BUSINESS_KEY',
            `sdk-smoke-${now()}`,
        ),
        messageName: read(
            'ARTIFICIALFLOW_MESSAGE_NAME',
            '<OPTIONAL_BPMN_MESSAGE_NAME>',
        ),
        messageCorrelationKey: read(
            'ARTIFICIALFLOW_MESSAGE_CORRELATION_KEY',
            '<OPTIONAL_MESSAGE_CORRELATION_KEY>',
        ),
        workerJobType: read(
            'ARTIFICIALFLOW_WORKER_JOB_TYPE',
            '<OPTIONAL_SERVICE_TASK_JOB_TYPE>',
        ),
    };
}
