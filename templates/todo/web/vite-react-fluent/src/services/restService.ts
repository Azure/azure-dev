import axios, { AxiosInstance } from 'axios';

export interface QueryOptions {
    top?: number;
    skip?: number;
}

export interface Entity {
    id?: string;
    created?: Date;
    updated?: Date
}

export abstract class RestService<T extends Entity> {
    protected client: AxiosInstance;

    public constructor(baseUrl: string, baseRoute: string) {
        this.client = axios.create({
            baseURL: `${baseUrl}${baseRoute}`
        });
    }

    public async getList(queryOptions?: QueryOptions): Promise<T[]> {
        const response = await this.client.request<T[]>({
            method: 'GET',
            data: queryOptions
        });

        return response.data;
    }

    public async get(id: string): Promise<T> {
        const response = await this.client.request<T>({
            method: 'GET',
            url: id
        });

        return response.data
    }

    public async save(entity: T): Promise<T> {
        return entity.id
            ? await this.put(entity)
            : await this.post(entity);
    }

    public async delete(id: string): Promise<void> {
        await this.client.request<void>({
            method: 'DELETE',
            url: id
        });
    }

    private async post(entity: T): Promise<T> {
        const response = await this.client.request<T>({
            method: 'POST',
            data: entity
        });

        return response.data;
    }

    private async put(entity: T): Promise<T> {
        const response = await this.client.request<T>({
            method: 'PUT',
            url: entity.id,
            data: entity
        });

        return response.data;
    }

    public async patch(id: string, entity: Partial<T>): Promise<T> {
        const response = await this.client.request<T>({
            method: 'PATCH',
            url: id,
            data: entity
        });

        return response.data;
    }
}
