import express, { Request } from "express";
import mongoose from "mongoose";
import { PagingQueryParams } from "../routes/common";
import { TodoList, TodoListModel } from "../models/todoList";

const router = express.Router();

type TodoListPathParams = {
    listId: string
}

/**
 * Gets a list of Todo list
 */
router.get("/", async (req: Request<unknown, unknown, unknown, PagingQueryParams>, res) => {
    const query = TodoListModel.find();
    const skip = req.query.skip ? parseInt(req.query.skip) : 0;
    const top = req.query.top ? parseInt(req.query.top) : 20;
    const lists = await query
        .skip(skip)
        .limit(top)
        .exec();

    res.json(lists);
});

/**
 * Creates a new Todo list
 */
router.post("/", async (req: Request<unknown, unknown, TodoList>, res) => {
    try {
        let list = new TodoListModel(req.body);
        list = await list.save();

        res.setHeader("location", `${req.protocol}://${req.get("Host")}/lists/${list.id}`);
        res.status(201).json(list);
    }
    catch (err: any) {
        switch (err.constructor) {
        case mongoose.Error.CastError:
        case mongoose.Error.ValidationError:
            return res.status(400).json(err.errors);
        default:
            throw err;
        }
    }
});

/**
 * Gets a Todo list with the specified ID
 */
router.get("/:listId", async (req: Request<TodoListPathParams>, res) => {
    try {
        const list = await TodoListModel
            .findById(req.params.listId)
            .orFail()
            .exec();

        res.json(list);
    }
    catch (err: any) {
        switch (err.constructor) {
        case mongoose.Error.CastError:
        case mongoose.Error.DocumentNotFoundError:
            return res.status(404).send();
        default:
            throw err;
        }
    }
});

/**
 * Updates a Todo list with the specified ID
 */
router.put("/:listId", async (req: Request<TodoListPathParams, unknown, TodoList>, res) => {
    try {
        const list: TodoList = {
            ...req.body,
            id: req.params.listId
        };

        await TodoListModel.validate(list);
        const updated = await TodoListModel
            .findOneAndUpdate({ _id: list.id }, list, { new: true })
            .orFail()
            .exec();

        res.json(updated);
    }
    catch (err: any) {
        switch (err.constructor) {
        case mongoose.Error.ValidationError:
            return res.status(400).json(err.errors);
        case mongoose.Error.CastError:
        case mongoose.Error.DocumentNotFoundError:
            return res.status(404).send();
        default:
            throw err;
        }
    }
});

/**
 * Deletes a Todo list with the specified ID
 */
router.delete("/:listId", async (req: Request<TodoListPathParams>, res) => {
    try {
        await TodoListModel
            .findByIdAndDelete(req.params.listId, {})
            .orFail()
            .exec();

        res.status(204).send();
    }
    catch (err: any) {
        switch (err.constructor) {
        case mongoose.Error.CastError:
        case mongoose.Error.DocumentNotFoundError:
            return res.status(404).send();
        default:
            throw err;
        }
    }
});

export default router;