import { ComponentType, ComponentClass } from 'react';
import { reactPlugin } from '../services/telemetryService';
import { withAITracking } from '@microsoft/applicationinsights-react-js';


const withApplicationInsights = (component: ComponentType<unknown>, componentName: string): ComponentClass<ComponentType<unknown>, unknown> => withAITracking<typeof component>(reactPlugin, component, componentName);
 
export default withApplicationInsights;
