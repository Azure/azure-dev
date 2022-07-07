import React, { FC, ReactElement } from 'react';
import TodoListMenu from '../components/todoListMenu';
import { TodoList } from '../models/todoList';

interface SidebarProps {
    selectedList?: TodoList
    lists?: TodoList[];
    onListCreate: (list: TodoList) => void
}

const Sidebar: FC<SidebarProps> = (props: SidebarProps): ReactElement => {
    return (
        <div>
            <TodoListMenu
                selectedList={props.selectedList}
                lists={props.lists}
                onCreate={props.onListCreate} />
        </div>
    );
};

export default Sidebar;