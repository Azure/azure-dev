import request from "supertest";
import { Server } from "http";
import { Express } from "express";
import { createApp } from "../app";
import { TodoItem, TodoItemState } from "../models/todoItem";
import { TodoList } from "../models/todoList";

describe("API", () => {
    let app: Express;
    let server: Server;

    beforeAll(async () => {
        app = await createApp();
        const port = process.env.PORT || 3100;

        server = app.listen(port, () => {
            console.log(`Started listening on port ${port}`);
        });
    });

    afterAll((done) => {
        server.close(done);
        console.log("Stopped server");
    });

    describe("Todo List Routes", () => {
        it("can GET an array of lists", async () => {
            const todoList: Partial<TodoList> = {
                name: "GET all test",
                description: "GET all description"
            };

            let res = await createList(todoList);
            const newList = res.body as TodoList;

            res = await getLists();

            expect(res.statusCode).toEqual(200);
            expect(res.body.length).toBeGreaterThan(0);

            await deleteList(newList.id);
        });

        it("can GET an array of lists with paging", async () => {
            const todoList: Partial<TodoList> = {
                name: "GET paging test",
                description: "GET paging description"
            };

            let res = await createList(todoList);
            const list1 = res.body as TodoList;
            res = await createList(todoList);
            const list2 = res.body as TodoList;

            res = await getLists("top=1&skip=1");

            expect(res.statusCode).toEqual(200);
            expect(res.body).toHaveLength(1);

            await deleteList(list1.id);
            await deleteList(list2.id);
        });

        it("can GET a list by unique id", async () => {
            const todoList: Partial<TodoList> = {
                name: "GET by id test",
                description: "GET by id description"
            };

            let res = await createList(todoList);
            const newList = res.body as TodoList;
            res = await getList(res.body.id);

            expect(res.statusCode).toEqual(200);
            expect(res.body).toMatchObject(newList);

            await deleteList(newList.id);
        });

        it("can POST (create) new list", async () => {
            const todoList: Partial<TodoList> = {
                name: "POST test",
                description: "POST description"
            };

            const res = await createList(todoList);

            expect(res.statusCode).toEqual(201);
            expect(res.body).toMatchObject({
                ...todoList,
                id: expect.any(String),
                createdDate: expect.any(String),
                updatedDate: expect.any(String)
            });

            await deleteList(res.body.id);
        });

        it("can PUT (update) lists", async () => {
            const todoList: Partial<TodoList> = {
                name: "PUT test",
                description: "PUT description"
            };

            let res = await createList(todoList);
            const listToUpdate: Partial<TodoList> = {
                ...res.body,
                name: "PUT test (updated)"
            };

            res = await updateList(res.body.id, listToUpdate);

            expect(res.statusCode).toEqual(200);
            expect(res.body).toMatchObject({
                id: listToUpdate.id,
                name: listToUpdate.name
            });

            await deleteList(res.body.id);
        });

        it("can DELETE lists", async () => {
            const todoList: Partial<TodoList> = {
                name: "PUT test",
                description: "PUT description"
            };

            let res = await createList(todoList);
            const newList = res.body as TodoList;
            res = await deleteList(newList.id);

            expect(res.statusCode).toEqual(204);

            res = await getList(newList.id);
            expect(res.statusCode).toEqual(404);
        });
    });

    describe("Todo Item Routes", () => {
        let testList: TodoList;

        beforeAll(async () => {
            const res = await createList({ name: "Integration test" });
            testList = res.body as TodoList;
        });

        afterAll(async () => {
            await deleteList(testList.id);
        });

        it("can GET an array of items", async () => {
            const todoItem: Partial<TodoItem> = {
                name: "GET all test",
                description: "GET all description"
            };

            let res = await createItem(testList.id, todoItem);
            const newItem = res.body as TodoItem;

            res = await getItems(testList.id);

            expect(res.statusCode).toEqual(200);
            expect(res.body).toHaveLength(1);

            await deleteItem(newItem.listId.toString(), newItem.id.toString());
        });

        it("can GET an array of items with paging", async () => {
            const todoItem: Partial<TodoItem> = {
                name: "GET paging test",
                description: "GET paging description"
            };

            let res = await createItem(testList.id, todoItem);
            const item1 = res.body as TodoItem;
            res = await createItem(testList.id, todoItem);
            const item2 = res.body as TodoItem;

            res = await getItems(testList.id, "top=1&skip=1");

            expect(res.statusCode).toEqual(200);
            expect(res.body).toHaveLength(1);

            await deleteItem(item1.listId.toString(), item1.id.toString());
            await deleteItem(item2.listId.toString(), item2.id.toString());
        });

        it("can GET an array of items by state", async () => {
            const item1: Partial<TodoItem> = {
                name: "GET state test (todo)",
                description: "GET paging description",
                state: TodoItemState.Todo
            };

            const item2: Partial<TodoItem> = {
                name: "GET state test (inprogress)",
                description: "GET paging description",
                state: TodoItemState.InProgress
            };

            let res = await createItem(testList.id, item1);
            const newItem1 = res.body as TodoItem;
            res = await createItem(testList.id, item2);
            const newItem2 = res.body as TodoItem;

            res = await getItems(testList.id, "", TodoItemState.Todo);

            expect(res.statusCode).toEqual(200);
            expect(res.body.length).toEqual(1); // Expect only 1 item to be in the TODO state.

            await deleteItem(newItem1.listId.toString(), newItem1.id.toString());
            await deleteItem(newItem2.listId.toString(), newItem2.id.toString());
        });

        it("can GET an item by unique id", async () => {
            const todoItem: Partial<TodoItem> = {
                name: "GET by id test",
                description: "GET by id description"
            };

            let res = await createItem(testList.id, todoItem);
            const newItem = res.body as TodoItem;
            res = await getItem(newItem.listId.toString(), newItem.id.toString());

            expect(res.statusCode).toEqual(200);
            expect(res.body).toMatchObject(newItem);

            await deleteItem(newItem.listId.toString(), newItem.id.toString());
        });

        it("can POST (create) new item", async () => {
            const todoItem: Partial<TodoItem> = {
                name: "POST test",
                description: "POST description",
                state: TodoItemState.Todo
            };

            const res = await createItem(testList.id, todoItem);

            expect(res.statusCode).toEqual(201);
            expect(res.body).toMatchObject({
                ...todoItem,
                id: expect.any(String),
                createdDate: expect.any(String),
                updatedDate: expect.any(String)
            });

            await deleteItem(res.body.listId, res.body.id);
        });

        it("can PUT (update) items", async () => {
            const todoItem: Partial<TodoItem> = {
                name: "PUT test",
                description: "PUT description"
            };

            let res = await createItem(testList.id, todoItem);
            const itemToUpdate: TodoItem = {
                ...res.body,
                name: "PUT test (updated)",
                state: TodoItemState.InProgress
            };

            res = await updateItem(itemToUpdate.listId.toString(), itemToUpdate.id.toString(), itemToUpdate);

            expect(res.statusCode).toEqual(200);
            expect(res.body).toMatchObject({
                id: itemToUpdate.id,
                name: itemToUpdate.name,
                state: itemToUpdate.state
            });

            await deleteItem(res.body.listId, res.body.id);
        });

        it("can DELETE items", async () => {
            const todoItem: Partial<TodoItem> = {
                name: "PUT test",
                description: "PUT description"
            };

            let res = await createItem(testList.id, todoItem);
            const newItem = res.body as TodoItem;
            res = await deleteItem(newItem.listId.toString(), newItem.id.toString());

            expect(res.statusCode).toEqual(204);

            res = await getItem(newItem.listId.toString(), newItem.id.toString());
            expect(res.statusCode).toEqual(404);
        });
    });

    const getLists = (query = "") => {
        return request(app)
            .get("/lists")
            .query(query)
            .send();
    };

    const getList = (listId: string) => {
        return request(app)
            .get(`/lists/${listId}`)
            .send();
    };

    const createList = (list: Partial<TodoList>) => {
        return request(app)
            .post("/lists")
            .send(list);
    };

    const updateList = (listId: string, list: Partial<TodoList>) => {
        return request(app)
            .put(`/lists/${listId}`)
            .send(list);
    };

    const deleteList = (listId: string) => {
        return request(app)
            .delete(`/lists/${listId}`)
            .send();
    };

    const getItems = (listId: string, query = "", state?: TodoItemState) => {
        const path = state
            ? `/lists/${listId}/items/state/${state.toString()}`
            : `/lists/${listId}/items`;

        return request(app)
            .get(path)
            .query(query)
            .send();
    };

    const getItem = (listId: string, itemId: string) => {
        return request(app)
            .get(`/lists/${listId}/items/${itemId}`)
            .send();
    };

    const createItem = (listId: string, item: Partial<TodoItem>) => {
        return request(app)
            .post(`/lists/${listId}/items`)
            .send(item);
    };

    const updateItem = (listId: string, itemId: string, item: Partial<TodoItem>) => {
        return request(app)
            .put(`/lists/${listId}/items/${itemId}`)
            .send(item);
    };

    const deleteItem = (listId: string, itemId: string) => {
        return request(app)
            .delete(`/lists/${listId}/items/${itemId}`)
            .send();
    };
});
