import { Text, DatePicker, Stack, TextField, PrimaryButton, DefaultButton, Dropdown, IDropdownOption, FontIcon } from '@fluentui/react';
import React, { useEffect, useState, FC, ReactElement, MouseEvent, FormEvent } from 'react';
import { TodoItem, TodoItemState } from '../models';
import { stackGaps, stackItemMargin, stackItemPadding, titleStackStyles } from '../ux/styles';

interface TodoItemDetailPaneProps {
    item?: TodoItem;
    onEdit: (item: TodoItem) => void
    onCancel: () => void
}

export const TodoItemDetailPane: FC<TodoItemDetailPaneProps> = (props: TodoItemDetailPaneProps): ReactElement => {
    const [name, setName] = useState(props.item?.name || '');
    const [description, setDescription] = useState(props.item?.description);
    const [dueDate, setDueDate] = useState(props.item?.dueDate);
    const [state, setState] = useState(props.item?.state || TodoItemState.Todo);

    useEffect(() => {
        setName(props.item?.name || '');
        setDescription(props.item?.description);
        setDueDate(props.item?.dueDate ? new Date(props.item?.dueDate) : undefined);
        setState(props.item?.state || TodoItemState.Todo);
    }, [props.item]);

    const saveTodoItem = (evt: MouseEvent<HTMLButtonElement>) => {
        evt.preventDefault();

        if (!props.item?.id) {
            return;
        }

        const todoItem: TodoItem = {
            id: props.item.id,
            listId: props.item.listId,
            name: name,
            description: description,
            dueDate: dueDate,
            state: state,
        };

        props.onEdit(todoItem);
    };

    const cancelEdit = (evt: MouseEvent<HTMLButtonElement>) => {
        props.onCancel();
    }

    const onStateChange = (evt: FormEvent<HTMLDivElement>, value?: IDropdownOption) => {
        if (value) {
            setState(value.key as TodoItemState);
        }
    }

    const onDueDateChange = (date: Date | null | undefined) => {
        setDueDate(date || undefined);
    }

    const todoStateOptions: IDropdownOption[] = [
        { key: TodoItemState.Todo, text: 'To Do' },
        { key: TodoItemState.InProgress, text: 'In Progress' },
        { key: TodoItemState.Done, text: 'Done' },
    ];

    return (
        <Stack>
            {props.item &&
                <>
                    <Stack.Item styles={titleStackStyles} tokens={stackItemPadding}>
                        <Text block variant="xLarge">{name}</Text>
                        <Text variant="small">{description}</Text>
                    </Stack.Item>
                    <Stack.Item tokens={stackItemMargin}>
                        <TextField label="Name" placeholder="Item name" required value={name} onChange={(e, value) => setName(value || '')} />
                        <TextField label="Description" placeholder="Item description" multiline size={20} value={description || ''} onChange={(e, value) => setDescription(value)} />
                        <Dropdown label="State" options={todoStateOptions} required selectedKey={state} onChange={onStateChange} />
                        <DatePicker label="Due Date" placeholder="Due date" value={dueDate} onSelectDate={onDueDateChange} />
                    </Stack.Item>
                    <Stack.Item tokens={stackItemMargin}>
                        <Stack horizontal tokens={stackGaps}>
                            <PrimaryButton text="Save" onClick={saveTodoItem} />
                            <DefaultButton text="Cancel" onClick={cancelEdit} />
                        </Stack>
                    </Stack.Item>
                </>
            }
            {!props.item &&
                <Stack.Item tokens={stackItemPadding} style={{ textAlign: "center" }} align="center">
                    <FontIcon iconName="WorkItem" style={{ fontSize: 24, padding: 20 }} />
                    <Text block>Select an item to edit</Text>
                </Stack.Item>}
        </Stack >
    );
}

export default TodoItemDetailPane;