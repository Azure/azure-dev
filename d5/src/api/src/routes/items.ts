import express from "express";
import mongoose from "mongoose";
import { Request } from "express";
import { PagingQueryParams } from "../routes/common";
import { TodoItem, TodoItemModel, TodoItemState } from "../models/todoItem";

const router = express.Router({ mergeParams: true });

type TodoItemPathParams = {
    listId: mongoose.Types.ObjectId
    itemId: mongoose.Types.ObjectId
    state?: TodoItemState
}

/**
 * Gets a list of Todo item within a list
 */
router.get("/", async (req: Request<TodoItemPathParams, unknown, unknown, PagingQueryParams>, res) => {
    const query = TodoItemModel.find({ listId: req.params.listId });
    const skip = req.query.skip ? parseInt(req.query.skip) : 0;
    const top = req.query.top ? parseInt(req.query.top) : 20;
    const lists = await query
        .skip(skip)
        .limit(top)
        .exec();

    res.json(lists);
});

/**
 * Creates a new Todo item within a list
 */
router.post("/", async (req: Request<TodoItemPathParams, unknown, TodoItem>, res) => {
    try {
        const item: TodoItem = {
            ...req.body,
            listId: req.params.listId
        };

        let newItem = new TodoItemModel(item);
        newItem = await newItem.save();

        res.setHeader("location", `${req.protocol}://${req.get("Host")}/lists/${req.params.listId}/${newItem.id}`);
        res.status(201).json(newItem);
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
 * Gets a Todo item with the specified ID within a list
 */
router.get("/:itemId", async (req: Request<TodoItemPathParams>, res) => {
    try {
        const list = await TodoItemModel
            .findOne({ _id: req.params.itemId, listId: req.params.listId })
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
 * Updates a Todo item with the specified ID within a list
 */
router.put("/:itemId", async (req: Request<TodoItemPathParams, unknown, TodoItem>, res) => {
    try {
        const item: TodoItem = {
            ...req.body,
            id: req.params.itemId,
            listId: req.params.listId
        };

        await TodoItemModel.validate(item);
        const updated = await TodoItemModel
            .findOneAndUpdate({ _id: item.id }, item, { new: true })
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
 * Deletes a Todo item with the specified ID within a list
 */
router.delete("/:itemId", async (req, res) => {
    try {
        await TodoItemModel
            .findByIdAndDelete(req.params.itemId, {})
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

/**
 * Get a list of items by state
 */
router.get("/state/:state", async (req: Request<TodoItemPathParams, unknown, unknown, PagingQueryParams>, res) => {
    const query = TodoItemModel.find({ listId: req.params.listId, state: req.params.state });
    const skip = req.query.skip ? parseInt(req.query.skip) : 0;
    const top = req.query.top ? parseInt(req.query.top) : 20;

    const lists = await query
        .skip(skip)
        .limit(top)
        .exec();

    res.json(lists);
});

router.put("/state/:state", async (req: Request<TodoItemPathParams, unknown, mongoose.Types.ObjectId[]>, res) => {
    try {
        const completedDate = req.params.state === TodoItemState.Done ? new Date() : undefined;

        const updateTasks = req.body.map(
            id => TodoItemModel
                .findOneAndUpdate(
                    { listId: req.params.listId, _id: id },
                    { state: req.params.state, completedDate: completedDate })
                .orFail()
                .exec()
        );

        await Promise.all(updateTasks);

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