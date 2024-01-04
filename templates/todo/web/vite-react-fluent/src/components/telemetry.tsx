import { FC, ReactElement, useEffect, PropsWithChildren } from 'react';
import { TelemetryProvider } from './telemetryContext';
import { reactPlugin, getApplicationInsights } from '../services/telemetryService';

type TelemetryProps = PropsWithChildren<unknown>;

const Telemetry: FC<TelemetryProps> = (props: TelemetryProps): ReactElement => {

    useEffect(() => {
        getApplicationInsights();
    }, []);

    return (
        <TelemetryProvider value={reactPlugin}>
            {props.children}
        </TelemetryProvider>
    );
}

export default Telemetry;
