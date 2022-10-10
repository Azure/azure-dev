import { localize } from '../localize';

export function withTimeout<T>(promise: Promise<T>, timeout: number, message?: string): Promise<T> {
    message ??= localize('azure-dev.utils.withTimeout', 'Timed out waiting for a task to complete.');

    return new Promise((resolve, reject) => {
        const timer = setTimeout(
            () => reject(new Error(message)),
            timeout);

        promise.then(
            (result) => {
                clearTimeout(timer);

                return resolve(result);
            },
            (err) => {
                clearTimeout(timer);

                return reject(err);
            }
        );
    });
}
