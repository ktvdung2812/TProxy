import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { modelOptionGroupLabel, type ModelOption } from "../../lib/modelOptions";
import { cn } from "../ui";

type Option = ModelOption;

type Props = {
  value: string;
  onChange: (value: string) => void;
  options: Option[];
  placeholder?: string;
  className?: string;
  "aria-label"?: string;
};

function optionTitle(option: Option): string {
  const label = option.label.trim();
  return label || option.value;
}

export function ModelTargetCombobox({
  value,
  onChange,
  options,
  placeholder,
  className,
  "aria-label": ariaLabel,
}: Props) {
  const listId = useId();
  const rootRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(-1);

  const selectedOption = useMemo(
    () => options.find((option) => option.value === value),
    [options, value],
  );

  const inputValue = open ? query : selectedOption ? optionTitle(selectedOption) : value;

  const filteredOptions = useMemo(() => {
    const needle = (open ? query : value).trim().toLowerCase();
    if (!needle) return options;
    return options.filter((option) => {
      const title = optionTitle(option).toLowerCase();
      return title.includes(needle) || option.value.toLowerCase().includes(needle);
    });
  }, [open, options, query, value]);

  const listItems = useMemo(() => {
    const items: Array<{ kind: "group"; key: string; label: string } | { kind: "option"; option: Option }> = [];
    let lastGroup = "";
    for (const option of filteredOptions) {
      if (option.group && option.group !== lastGroup) {
        items.push({ kind: "group", key: option.group, label: modelOptionGroupLabel(option.group) });
        lastGroup = option.group;
      }
      items.push({ kind: "option", option });
    }
    return items;
  }, [filteredOptions]);

  const selectableOptions = useMemo(
    () => listItems.filter((item): item is { kind: "option"; option: Option } => item.kind === "option"),
    [listItems],
  );

  useEffect(() => {
    if (!open) {
      setActiveIndex(-1);
      return;
    }
    if (selectableOptions.length === 0) {
      setActiveIndex(-1);
      return;
    }
    setActiveIndex((current) => (current >= 0 && current < selectableOptions.length ? current : 0));
  }, [open, selectableOptions]);

  useEffect(() => {
    const handlePointerDown = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, []);

  const openMenu = () => {
    setQuery(selectedOption ? optionTitle(selectedOption) : value);
    setOpen(true);
  };

  const selectOption = (option: Option) => {
    onChange(option.value);
    setQuery(optionTitle(option));
    setOpen(false);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (!open && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
      openMenu();
      return;
    }
    if (event.key === "Escape") {
      setOpen(false);
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      if (selectableOptions.length === 0) return;
      setActiveIndex((current) => (current + 1) % selectableOptions.length);
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      if (selectableOptions.length === 0) return;
      setActiveIndex((current) => (current <= 0 ? selectableOptions.length - 1 : current - 1));
      return;
    }
    if (event.key === "Enter" && open && activeIndex >= 0 && selectableOptions[activeIndex]) {
      event.preventDefault();
      selectOption(selectableOptions[activeIndex].option);
    }
  };

  let optionIndex = -1;

  return (
    <div className={cn("mapping-combobox", className)} ref={rootRef}>
      <input
        className="input mapping-combobox-input"
        role="combobox"
        aria-label={ariaLabel}
        aria-expanded={open}
        aria-controls={listId}
        aria-autocomplete="list"
        value={inputValue}
        placeholder={placeholder}
        onFocus={openMenu}
        onChange={(event) => {
          const next = event.target.value;
          setQuery(next);
          onChange(next);
          setOpen(true);
        }}
        onKeyDown={handleKeyDown}
      />
      {open && listItems.length > 0 ? (
        <ul className="mapping-combobox-list custom-scrollbar" id={listId} role="listbox">
          {listItems.map((item) => {
            if (item.kind === "group") {
              return (
                <li key={`group-${item.key}`} className="mapping-combobox-group" role="presentation">
                  {item.label}
                </li>
              );
            }
            optionIndex += 1;
            const title = optionTitle(item.option);
            return (
              <li key={item.option.value}>
                <button
                  type="button"
                  role="option"
                  aria-selected={value === item.option.value}
                  className={cn("mapping-combobox-option", optionIndex === activeIndex && "is-active")}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => selectOption(item.option)}
                >
                  <span className="mapping-combobox-option-label">{title}</span>
                </button>
              </li>
            );
          })}
        </ul>
      ) : null}
    </div>
  );
}
