import { IIconProps, INavLink, INavLinkGroup, Nav, Stack, TextField } from '@fluentui/react';
import { FC, ReactElement, useState, FormEvent, MouseEvent } from 'react';
import { useNavigate } from 'react-router';
import { TodoList } from '../models/todoList';
import { stackItemPadding } from '../ux/styles';

interface TodoListMenuProps {
    selectedList?: TodoList
    lists?: TodoList[]
    onCreate: (list: TodoList) => void
}

const iconProps: IIconProps = {
    iconName: 'AddToShoppingList'
}

const TodoListMenu: FC<TodoListMenuProps> = (props: TodoListMenuProps): ReactElement => {
    const navigate = useNavigate();
    const [newListName, setNewListName] = useState('');

    const onNavLinkClick = (evt?: MouseEvent<HTMLElement>, item?: INavLink) => {
        evt?.preventDefault();

        if (!item) {
            return;
        }

        navigate(`/lists/${item.key}`);
    }

    const createNavGroups = (lists: TodoList[]): INavLinkGroup[] => {
        const links = lists.map(list => ({
            key: list.id,
            name: list.name,
            url: `/lists/${list.id}`,
            links: [],
            isExpanded: props.selectedList ? list.id === props.selectedList.id : false
        }));

        return [{
            links: links
        }]
    }

    const onNewListNameChange = (_evt: FormEvent<HTMLInputElement | HTMLTextAreaElement>, value?: string) => {
        setNewListName(value || '');
    }

    const onFormSubmit = async (evt: FormEvent<HTMLFormElement>) => {
        evt.preventDefault();

        if (newListName) {
            const list: TodoList = {
                name: newListName
            };

            props.onCreate(list);
            setNewListName('');
        }
    }

    return (
        <Stack>
            <Stack.Item>
                <Nav
                    selectedKey={props.selectedList?.id}
                    onLinkClick={onNavLinkClick}
                    groups={createNavGroups(props.lists || [])} />
            </Stack.Item>
            <Stack.Item tokens={stackItemPadding}>
                <form onSubmit={onFormSubmit}>
                    <TextField
                        borderless
                        iconProps={iconProps}
                        value={newListName}
                        disabled={props.selectedList == null}
                        placeholder="New List"
                        onChange={onNewListNameChange} />
                </form>
            </Stack.Item>
        </Stack>
    );
};

export default TodoListMenu;