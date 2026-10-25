const INPUT_TYPES = {
  number: 'digit',
  phone: 'number',
  mobile: 'number',
  tel: 'number',
  id_card: 'idcard',
  idcard: 'idcard',
}

function normalizeOptions(rawOptions) {
  if (!Array.isArray(rawOptions)) {
    return []
  }
  return rawOptions
    .map((option) => {
      if (typeof option === 'string' || typeof option === 'number') {
        return { label: String(option), value: String(option) }
      }
      if (option && option.value !== undefined) {
        return {
          label: option.label !== undefined ? String(option.label) : String(option.value),
          value: String(option.value),
        }
      }
      return null
    })
    .filter(Boolean)
}

function normalizeFields(schema) {
  const rawFields = Array.isArray(schema)
    ? schema
    : schema && Array.isArray(schema.fields)
      ? schema.fields
      : []

  return rawFields
    .filter((field) => field && field.name)
    .map((field) => {
      const type = String(field.type || 'text').toLowerCase()
      const options = normalizeOptions(field.options)
      const defaultValue = type === 'checkbox'
        ? []
        : field.default !== undefined
          ? field.default
          : ''
      return {
        name: String(field.name),
        label: field.label || field.name,
        type,
        required: field.required === true,
        placeholder: field.placeholder || `请输入${field.label || field.name}`,
        maxlength: Number(field.maxlength || 200),
        inputType: INPUT_TYPES[type] || 'text',
        options,
        value: defaultValue,
        selectedIndex: 0,
        displayValue: '',
        selectPlaceholder: `请选择${field.label || field.name}`,
      }
    })
}

function applyDraft(fields, draftValues) {
  return fields.map((field) => {
    const hasValue = draftValues && Object.prototype.hasOwnProperty.call(draftValues, field.name)
    let value = hasValue ? draftValues[field.name] : field.value
    if (field.type === 'checkbox' && !Array.isArray(value)) {
      value = []
    }
    if (field.type === 'select') {
      const foundIndex = field.options.findIndex((option) => option.value === value)
      const selectedIndex = foundIndex >= 0 ? foundIndex : 0
      return {
        ...field,
        value: value === undefined || value === null ? '' : value,
        selectedIndex,
        displayValue: foundIndex >= 0 ? field.options[foundIndex].label : '',
      }
    }
    return {
      ...field,
      value: value === undefined || value === null ? '' : value,
    }
  })
}

function buildFormData(fields) {
  const data = {}
  fields.forEach((field) => {
    if (field.type === 'number' && field.value !== '' && field.value !== null) {
      const numberValue = Number(field.value)
      data[field.name] = Number.isNaN(numberValue) ? field.value : numberValue
      return
    }
    data[field.name] = field.value
  })
  return data
}

function isEmpty(value) {
  if (Array.isArray(value)) {
    return value.length === 0
  }
  return value === undefined || value === null || String(value).trim() === ''
}

function validateFields(fields) {
  for (let index = 0; index < fields.length; index += 1) {
    const field = fields[index]
    if (field.required && isEmpty(field.value)) {
      return { fieldIndex: index, message: `请填写${field.label}` }
    }
    if (isEmpty(field.value)) {
      continue
    }
    if (field.type === 'phone' || field.type === 'mobile' || field.type === 'tel') {
      if (!/^1[3-9]\d{9}$/.test(String(field.value))) {
        return { fieldIndex: index, message: `请填写正确的${field.label}` }
      }
    }
    if (field.type === 'id_card' || field.type === 'idcard') {
      if (!/^\d{15}$|^\d{17}[\dXx]$/.test(String(field.value))) {
        return { fieldIndex: index, message: `请填写正确的${field.label}` }
      }
    }
    if (field.type === 'number' && Number.isNaN(Number(field.value))) {
      return { fieldIndex: index, message: `${field.label}必须是数字` }
    }
  }
  return null
}

module.exports = {
  normalizeFields,
  applyDraft,
  buildFormData,
  validateFields,
}
