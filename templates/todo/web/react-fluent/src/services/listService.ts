import { RestService } from './restService';
import { TodoList } from '../models';

export class ListService extends RestService<TodoList> {
    public constructor(baseUrl: string, baseRoute: string) {
        super(baseUrl, baseRoute);
    }
}
