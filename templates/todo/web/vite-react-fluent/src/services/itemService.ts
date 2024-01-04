import { RestService } from './restService';
import { TodoItem } from '../models';

export class ItemService extends RestService<TodoItem> {
    public constructor(baseUrl: string, baseRoute: string) {
        super(baseUrl, baseRoute);
    }
}
